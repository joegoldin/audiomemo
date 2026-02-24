package transcribe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

type Deepgram struct {
	apiKey       string
	defaultModel string
	baseURL      string
}

func NewDeepgram(apiKey, defaultModel string) *Deepgram {
	return &Deepgram{
		apiKey:       apiKey,
		defaultModel: defaultModel,
		baseURL:      "https://api.deepgram.com",
	}
}

func (d *Deepgram) Name() string { return "deepgram" }

func (d *Deepgram) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if d.apiKey == "" {
		return nil, fmt.Errorf("deepgram API key not configured (set DEEPGRAM_API_KEY or config)")
	}

	if err := validateOpts(d.Name(), opts, true, true, true, true, true); err != nil {
		return nil, err
	}

	f, err := os.Open(audioPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer f.Close()

	query := d.buildQuery(opts)
	reqURL := fmt.Sprintf("%s/v1/listen?%s", d.baseURL, query.Encode())

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, f)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+d.apiKey)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepgram request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepgram API error (%d): %s", resp.StatusCode, string(body))
	}

	return d.parseResponse(body, opts.Diarize)
}

func (d *Deepgram) buildQuery(opts TranscribeOpts) url.Values {
	q := url.Values{}
	model := opts.Model
	if model == "" {
		model = d.defaultModel
	}
	q.Set("model", model)
	q.Set("utterances", "true")

	if opts.SmartFormat {
		q.Set("smart_format", "true")
	}
	if opts.Punctuate {
		q.Set("punctuate", "true")
	}
	if opts.Diarize {
		q.Set("diarize", "true")
	}
	if opts.FillerWords {
		q.Set("filler_words", "true")
	}
	if opts.Numerals {
		q.Set("numerals", "true")
	}

	if opts.Language != "" {
		q.Set("language", opts.Language)
	} else {
		q.Set("detect_language", "true")
	}
	return q
}

type deepgramResponse struct {
	Metadata struct {
		Duration float64 `json:"duration"`
	} `json:"metadata"`
	Results struct {
		Channels []struct {
			Alternatives []struct {
				Transcript string `json:"transcript"`
			} `json:"alternatives"`
		} `json:"channels"`
		Utterances []struct {
			Start      float64 `json:"start"`
			End        float64 `json:"end"`
			Transcript string  `json:"transcript"`
			Speaker    int     `json:"speaker"`
		} `json:"utterances"`
	} `json:"results"`
}

func (d *Deepgram) parseResponse(data []byte, diarize bool) (*Result, error) {
	var resp deepgramResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse deepgram response: %w", err)
	}

	result := &Result{Duration: resp.Metadata.Duration}

	if len(resp.Results.Channels) > 0 && len(resp.Results.Channels[0].Alternatives) > 0 {
		result.Text = resp.Results.Channels[0].Alternatives[0].Transcript
	}

	for _, u := range resp.Results.Utterances {
		seg := Segment{
			Start: u.Start,
			End:   u.End,
			Text:  u.Transcript,
		}
		if diarize {
			seg.Speaker = fmt.Sprintf("Speaker %d", u.Speaker)
		}
		result.Segments = append(result.Segments, seg)
	}

	return result, nil
}
