package transcribe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Whisper struct {
	binary       string
	defaultModel string
}

func NewWhisper(binary, defaultModel string) *Whisper {
	return &Whisper{binary: binary, defaultModel: defaultModel}
}

func (w *Whisper) Name() string { return "whisper" }

func (w *Whisper) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if _, err := exec.LookPath(w.binary); err != nil {
		return nil, fmt.Errorf("whisper binary %q not found on PATH: %w", w.binary, err)
	}
	if _, err := os.Stat(audioPath); err != nil {
		return nil, fmt.Errorf("audio file not found: %w", err)
	}

	args := w.buildArgs(audioPath, opts)
	cmd := exec.CommandContext(ctx, w.binary, args...)

	// whisper outputs to a file named <input>.json in the same dir
	// Use a temp dir for output
	tmpDir, err := os.MkdirTemp("", "audiotools-whisper-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	cmd.Args = append(cmd.Args, "--output_dir", tmpDir)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("whisper failed: %w", err)
	}

	// Find the output JSON file
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	jsonPath := filepath.Join(tmpDir, base+".json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read whisper output: %w", err)
	}

	return w.parseOutput(data)
}

func (w *Whisper) buildArgs(audioPath string, opts TranscribeOpts) []string {
	model := opts.Model
	if model == "" {
		model = w.defaultModel
	}

	args := []string{
		"--model", model,
		"--output-format", "json",
		audioPath,
	}

	if opts.Language != "" {
		args = append(args[:2], append([]string{"--language", opts.Language}, args[2:]...)...)
	}

	return args
}

type whisperOutput struct {
	Text     string           `json:"text"`
	Segments []whisperSegment `json:"segments"`
	Language string           `json:"language"`
}

type whisperSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func (w *Whisper) parseOutput(data []byte) (*Result, error) {
	var out whisperOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse whisper JSON: %w", err)
	}

	result := &Result{
		Text:     strings.TrimSpace(out.Text),
		Language: out.Language,
	}
	for _, seg := range out.Segments {
		result.Segments = append(result.Segments, Segment{
			Start: seg.Start,
			End:   seg.End,
			Text:  strings.TrimSpace(seg.Text),
		})
	}
	if len(result.Segments) > 0 {
		result.Duration = result.Segments[len(result.Segments)-1].End
	}
	return result, nil
}
