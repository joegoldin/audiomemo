package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ElevenLabs struct {
	apiKey       string
	defaultModel string
	baseURL      string
	storeInCloud bool
	// deleteRetryDelay is the initial backoff before retrying a 404 on
	// DELETE. The synchronous transcribe response returns a transcription_id
	// before the server has finished persisting it, so an immediate delete
	// races the writer and 404s.
	deleteRetryDelay time.Duration
}

func NewElevenLabs(apiKey, defaultModel string, storeInCloud bool) *ElevenLabs {
	return &ElevenLabs{
		apiKey:           apiKey,
		defaultModel:     defaultModel,
		baseURL:          "https://api.elevenlabs.io",
		storeInCloud:     storeInCloud,
		deleteRetryDelay: 500 * time.Millisecond,
	}
}

func (e *ElevenLabs) Name() string { return "elevenlabs" }

func (e *ElevenLabs) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if e.apiKey == "" {
		return nil, fmt.Errorf("ElevenLabs API key not configured (set ELEVENLABS_API_KEY or config)")
	}

	if err := validateOpts(e.Name(), opts, true, false, false, false, false); err != nil {
		return nil, err
	}

	body, contentType, err := e.buildMultipart(audioPath, opts)
	if err != nil {
		return nil, err
	}

	reqURL := fmt.Sprintf("%s/v1/speech-to-text", e.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("xi-api-key", e.apiKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("elevenlabs API error (%d): %s", resp.StatusCode, string(respBody))
	}

	result, transcriptionID, err := e.parseResponse(respBody, opts.Diarize)
	if err != nil {
		return nil, err
	}

	// Delete transcript from ElevenLabs unless user wants cloud storage.
	if !e.storeInCloud && transcriptionID != "" {
		e.deleteTranscript(ctx, transcriptionID)
	}

	return result, nil
}

func (e *ElevenLabs) buildMultipart(audioPath string, opts TranscribeOpts) (*bytes.Buffer, string, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open audio file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	model := opts.Model
	if model == "" {
		model = e.defaultModel
	}
	w.WriteField("model_id", model)

	if opts.Language != "" {
		w.WriteField("language_code", opts.Language)
	}

	if opts.Diarize {
		w.WriteField("diarize", "true")
	}

	w.WriteField("timestamps_granularity", "word")

	fw, err := w.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, "", err
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}

	return &buf, w.FormDataContentType(), nil
}

type elevenlabsResponse struct {
	LanguageCode        string             `json:"language_code"`
	LanguageProbability float64            `json:"language_probability"`
	Text                string             `json:"text"`
	Words               []elevenlabsWord   `json:"words"`
	TranscriptionID     *string            `json:"transcription_id"`
	AudioDurationSecs   *float64           `json:"audio_duration_secs"`
}

type elevenlabsWord struct {
	Text      string   `json:"text"`
	Start     *float64 `json:"start"`
	End       *float64 `json:"end"`
	Type      string   `json:"type"` // "word", "spacing", "audio_event"
	SpeakerID *string  `json:"speaker_id"`
}

func (e *ElevenLabs) parseResponse(data []byte, diarize bool) (*Result, string, error) {
	var resp elevenlabsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, "", fmt.Errorf("failed to parse elevenlabs response: %w", err)
	}

	result := &Result{
		Text:     resp.Text,
		Language: resp.LanguageCode,
	}
	if resp.AudioDurationSecs != nil {
		result.Duration = *resp.AudioDurationSecs
	}

	if diarize {
		result.Segments = groupWordsBySpeaker(resp.Words)
	} else if len(resp.Words) > 0 {
		// Build segments from words for timing info even without diarization.
		result.Segments = groupWordsIntoSegments(resp.Words)
	}

	transcriptionID := ""
	if resp.TranscriptionID != nil {
		transcriptionID = *resp.TranscriptionID
	}

	return result, transcriptionID, nil
}

// groupWordsBySpeaker groups consecutive words by speaker_id into segments.
func groupWordsBySpeaker(words []elevenlabsWord) []Segment {
	var segments []Segment
	var current *Segment
	currentSpeaker := ""

	for _, w := range words {
		if w.Type == "audio_event" {
			continue
		}

		speaker := ""
		if w.SpeakerID != nil {
			speaker = *w.SpeakerID
		}

		if current == nil || speaker != currentSpeaker {
			if current != nil {
				current.Text = strings.TrimSpace(current.Text)
				segments = append(segments, *current)
			}
			start := 0.0
			if w.Start != nil {
				start = *w.Start
			}
			label := ""
			if speaker != "" {
				label = "Speaker " + speaker
			}
			current = &Segment{
				Start:   start,
				Speaker: label,
			}
			currentSpeaker = speaker
		}

		if w.Type == "spacing" {
			current.Text += w.Text
		} else {
			current.Text += w.Text
		}
		if w.End != nil {
			current.End = *w.End
		}
	}

	if current != nil {
		current.Text = strings.TrimSpace(current.Text)
		if current.Text != "" {
			segments = append(segments, *current)
		}
	}

	return segments
}

// groupWordsIntoSegments creates a single segment from all words (no speaker info).
func groupWordsIntoSegments(words []elevenlabsWord) []Segment {
	var start, end float64
	startSet := false
	for _, w := range words {
		if w.Start != nil && !startSet {
			start = *w.Start
			startSet = true
		}
		if w.End != nil {
			end = *w.End
		}
	}
	return []Segment{{Start: start, End: end, Text: strings.TrimSpace(buildTextFromWords(words))}}
}

func buildTextFromWords(words []elevenlabsWord) string {
	var b strings.Builder
	for _, w := range words {
		if w.Type == "audio_event" {
			continue
		}
		b.WriteString(w.Text)
	}
	return b.String()
}

// deleteTranscript removes a transcript from ElevenLabs cloud storage.
// On 404 we retry with backoff: the API persists transcripts asynchronously,
// so the just-returned transcription_id may not be findable for a moment.
// Errors are logged but not returned since the transcription itself succeeded.
func (e *ElevenLabs) deleteTranscript(ctx context.Context, transcriptionID string) {
	reqURL := fmt.Sprintf("%s/v1/speech-to-text/transcripts/%s", e.baseURL, transcriptionID)
	const maxAttempts = 5

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := e.deleteRetryDelay * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "DELETE", reqURL, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create delete request for ElevenLabs transcript: %v\n", err)
			return
		}
		req.Header.Set("xi-api-key", e.apiKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to delete ElevenLabs transcript %s: %v\n", transcriptionID, err)
			return
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			continue
		}
		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "Warning: failed to delete ElevenLabs transcript %s (HTTP %d)\n", transcriptionID, resp.StatusCode)
		}
		return
	}
}
