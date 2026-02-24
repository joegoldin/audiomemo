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
)

type Mistral struct {
	apiKey       string
	defaultModel string
	baseURL      string
}

func NewMistral(apiKey, defaultModel string) *Mistral {
	return &Mistral{
		apiKey:       apiKey,
		defaultModel: defaultModel,
		baseURL:      "https://api.mistral.ai",
	}
}

func (m *Mistral) Name() string { return "mistral" }

func (m *Mistral) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("Mistral API key not configured (set MISTRAL_API_KEY or config)")
	}

	if err := validateOpts(m.Name(), opts, false, false, false, false, false); err != nil {
		return nil, err
	}

	body, contentType, err := m.buildMultipart(audioPath, opts)
	if err != nil {
		return nil, err
	}

	reqURL := fmt.Sprintf("%s/v1/audio/transcriptions", m.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mistral request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mistral API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return m.parseResponse(respBody)
}

func (m *Mistral) buildMultipart(audioPath string, opts TranscribeOpts) (*bytes.Buffer, string, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open audio file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fw, err := w.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, "", err
	}

	model := opts.Model
	if model == "" {
		model = m.defaultModel
	}
	w.WriteField("model", model)

	if opts.Language != "" {
		w.WriteField("language", opts.Language)
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}

	return &buf, w.FormDataContentType(), nil
}

type mistralResponse struct {
	Model    string           `json:"model"`
	Text     string           `json:"text"`
	Language string           `json:"language"`
	Segments []mistralSegment `json:"segments"`
}

type mistralSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func (m *Mistral) parseResponse(data []byte) (*Result, error) {
	var resp mistralResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse mistral response: %w", err)
	}

	result := &Result{
		Text:     resp.Text,
		Language: resp.Language,
	}
	for _, seg := range resp.Segments {
		result.Segments = append(result.Segments, Segment{
			Start: seg.Start,
			End:   seg.End,
			Text:  seg.Text,
		})
	}
	if len(result.Segments) > 0 {
		result.Duration = result.Segments[len(result.Segments)-1].End
	}
	return result, nil
}
