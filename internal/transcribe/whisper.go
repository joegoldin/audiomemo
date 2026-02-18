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

// whisperVariant identifies which whisper implementation we're using.
type whisperVariant int

const (
	variantWhisper    whisperVariant = iota // OpenAI Python whisper
	variantWhisperCPP                      // whisper.cpp (whisper-cli)
	variantWhisperX                        // whisperx
)

// whisperBinaries lists binaries to search for, in priority order.
var whisperBinaries = []struct {
	name    string
	variant whisperVariant
}{
	{"whisper-cli", variantWhisperCPP},
	{"whisper", variantWhisper},
	{"whisperx", variantWhisperX},
}

type Whisper struct {
	binary       string
	variant      whisperVariant
	defaultModel string
}

// NewWhisper creates a whisper backend with a specific binary.
func NewWhisper(binary, defaultModel string) *Whisper {
	variant := detectVariant(binary)
	return &Whisper{binary: binary, defaultModel: defaultModel, variant: variant}
}

func detectVariant(binary string) whisperVariant {
	base := filepath.Base(binary)
	switch {
	case strings.Contains(base, "whisper-cli"):
		return variantWhisperCPP
	case strings.Contains(base, "whisperx"):
		return variantWhisperX
	default:
		return variantWhisper
	}
}

// DetectWhisper searches PATH for any whisper binary and returns a configured backend.
func DetectWhisper(defaultModel string) (*Whisper, bool) {
	for _, b := range whisperBinaries {
		if path, err := exec.LookPath(b.name); err == nil {
			return &Whisper{binary: path, variant: b.variant, defaultModel: defaultModel}, true
		}
	}
	return nil, false
}

func (w *Whisper) Name() string {
	switch w.variant {
	case variantWhisperCPP:
		return "whisper-cpp"
	case variantWhisperX:
		return "whisperx"
	default:
		return "whisper"
	}
}

func (w *Whisper) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if _, err := exec.LookPath(w.binary); err != nil {
		return nil, fmt.Errorf("whisper binary %q not found on PATH: %w", w.binary, err)
	}
	if _, err := os.Stat(audioPath); err != nil {
		return nil, fmt.Errorf("audio file not found: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "audiotools-whisper-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	args := w.buildArgs(audioPath, tmpDir, opts)
	cmd := exec.CommandContext(ctx, w.binary, args...)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s failed: %w", w.Name(), err)
	}

	jsonPath := w.findOutputJSON(audioPath, tmpDir)
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s output at %s: %w", w.Name(), jsonPath, err)
	}

	return w.parseOutput(data)
}

func (w *Whisper) buildArgs(audioPath, tmpDir string, opts TranscribeOpts) []string {
	model := opts.Model
	if model == "" {
		model = w.defaultModel
	}

	switch w.variant {
	case variantWhisperCPP:
		return w.buildWhisperCPPArgs(audioPath, tmpDir, model, opts)
	case variantWhisperX:
		return w.buildWhisperXArgs(audioPath, tmpDir, model, opts)
	default:
		return w.buildWhisperArgs(audioPath, tmpDir, model, opts)
	}
}

// OpenAI Python whisper: whisper --model base --output_format json --output_dir DIR file.ogg
func (w *Whisper) buildWhisperArgs(audioPath, tmpDir, model string, opts TranscribeOpts) []string {
	args := []string{
		"--model", model,
		"--output_format", "json",
		"--output_dir", tmpDir,
	}
	if opts.Language != "" {
		args = append(args, "--language", opts.Language)
	}
	args = append(args, audioPath)
	return args
}

// whisper.cpp: whisper-cli -m MODEL_PATH -oj -of DIR/basename -l en -f file.ogg
func (w *Whisper) buildWhisperCPPArgs(audioPath, tmpDir, model string, opts TranscribeOpts) []string {
	modelPath := resolveWhisperCPPModel(model)
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	outputPrefix := filepath.Join(tmpDir, base)

	args := []string{
		"-m", modelPath,
		"-oj",
		"-of", outputPrefix,
		"-np",
	}
	if opts.Language != "" {
		args = append(args, "-l", opts.Language)
	}
	args = append(args, "-f", audioPath)
	return args
}

// whisperx: whisperx --model base --output_format json --output_dir DIR file.ogg
func (w *Whisper) buildWhisperXArgs(audioPath, tmpDir, model string, opts TranscribeOpts) []string {
	args := []string{
		"--model", model,
		"--output_format", "json",
		"--output_dir", tmpDir,
	}
	if opts.Language != "" {
		args = append(args, "--language", opts.Language)
	}
	args = append(args, audioPath)
	return args
}

// resolveWhisperCPPModel converts a model name like "base" to a ggml model file path.
// It checks XDG_DATA_HOME/whisper-cpp/ and common locations.
func resolveWhisperCPPModel(model string) string {
	// If it's already a path, use it directly
	if strings.Contains(model, "/") || strings.HasSuffix(model, ".bin") {
		return model
	}

	filename := fmt.Sprintf("ggml-%s.bin", model)

	// Check XDG data dir
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dataDir = filepath.Join(home, ".local", "share")
		}
	}
	candidates := []string{
		filepath.Join(dataDir, "whisper-cpp", filename),
		filepath.Join(dataDir, "whisper", filename),
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Fall back to model name; whisper-cli will error with a clear message
	return filename
}

func (w *Whisper) findOutputJSON(audioPath, tmpDir string) string {
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	return filepath.Join(tmpDir, base+".json")
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
