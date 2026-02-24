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

type OpenAI struct {
	apiKey       string
	defaultModel string
	baseURL      string
}

func NewOpenAI(apiKey, defaultModel string) *OpenAI {
	return &OpenAI{
		apiKey:       apiKey,
		defaultModel: defaultModel,
		baseURL:      "https://api.openai.com",
	}
}

func (o *OpenAI) Name() string { return "openai" }

func (o *OpenAI) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if o.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key not configured (set OPENAI_API_KEY or config)")
	}

	if err := validateOpts(o.Name(), opts, false, false, false, false, false); err != nil {
		return nil, err
	}

	body, contentType, err := o.buildMultipart(audioPath, opts)
	if err != nil {
		return nil, err
	}

	reqURL := fmt.Sprintf("%s/v1/audio/transcriptions", o.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return o.parseVerboseResponse(respBody)
}

func (o *OpenAI) buildMultipart(audioPath string, opts TranscribeOpts) (*bytes.Buffer, string, error) {
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
		model = o.defaultModel
	}
	w.WriteField("model", model)
	w.WriteField("response_format", "verbose_json")

	if opts.Language != "" {
		w.WriteField("language", opts.Language)
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}

	return &buf, w.FormDataContentType(), nil
}

type openaiVerboseResponse struct {
	Text     string          `json:"text"`
	Language string          `json:"language"`
	Duration float64         `json:"duration"`
	Segments []openaiSegment `json:"segments"`
}

type openaiSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func (o *OpenAI) parseVerboseResponse(data []byte) (*Result, error) {
	var resp openaiVerboseResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse openai response: %w", err)
	}

	result := &Result{
		Text:     resp.Text,
		Language: resp.Language,
		Duration: resp.Duration,
	}
	for _, seg := range resp.Segments {
		result.Segments = append(result.Segments, Segment{
			Start: seg.Start,
			End:   seg.End,
			Text:  seg.Text,
		})
	}
	return result, nil
}
