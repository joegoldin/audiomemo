package transcribe

import (
	"bufio"
	"bytes"
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
	variantWhisperCPP    whisperVariant = iota // whisper.cpp (whisper-cli)
	variantWhisper                             // OpenAI Python whisper
	variantWhisperX                            // whisperx
	variantFFmpegWhisper                       // ffmpeg -af whisper (8.0+)
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
	case strings.Contains(base, "ffmpeg"):
		return variantFFmpegWhisper
	default:
		return variantWhisper
	}
}

// DetectWhisper searches PATH for any whisper binary and returns a configured backend.
// Priority: whisper-cli > whisper > whisperx > ffmpeg whisper filter.
func DetectWhisper(defaultModel string) (*Whisper, bool) {
	for _, b := range whisperBinaries {
		if path, err := exec.LookPath(b.name); err == nil {
			return &Whisper{binary: path, variant: b.variant, defaultModel: defaultModel}, true
		}
	}

	// Last resort: check if ffmpeg has the whisper filter (8.0+)
	if ffmpegPath, err := exec.LookPath("ffmpeg"); err == nil {
		if ffmpegHasWhisperFilter(ffmpegPath) {
			return &Whisper{binary: ffmpegPath, variant: variantFFmpegWhisper, defaultModel: defaultModel}, true
		}
	}

	return nil, false
}

// ffmpegHasWhisperFilter checks if ffmpeg was built with --enable-whisper.
func ffmpegHasWhisperFilter(ffmpegPath string) bool {
	out, err := exec.Command(ffmpegPath, "-filters").CombinedOutput()
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "whisper") {
			return true
		}
	}
	return false
}

func (w *Whisper) Name() string {
	switch w.variant {
	case variantWhisperCPP:
		return "whisper-cpp"
	case variantWhisperX:
		return "whisperx"
	case variantFFmpegWhisper:
		return "ffmpeg-whisper"
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

	if w.variant == variantFFmpegWhisper {
		return w.transcribeFFmpeg(ctx, audioPath, tmpDir, opts)
	}

	// whisper-cpp's nixpkgs build lacks ogg/opus codec support;
	// convert non-wav audio to 16kHz mono wav via ffmpeg as a workaround
	inputPath := audioPath
	if w.variant == variantWhisperCPP && !isWav(audioPath) {
		wavPath, err := convertToWav(ctx, audioPath, tmpDir, opts.Verbose)
		if err != nil {
			return nil, fmt.Errorf("failed to convert audio for whisper-cpp: %w", err)
		}
		inputPath = wavPath
	}

	args := w.buildArgs(inputPath, tmpDir, opts)
	cmd := exec.CommandContext(ctx, w.binary, args...)
	if opts.Verbose {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s failed: %w", w.Name(), err)
	}

	jsonPath := w.findOutputJSON(inputPath, tmpDir)
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s output at %s: %w", w.Name(), jsonPath, err)
	}

	return w.parseOutput(data)
}

// transcribeFFmpeg uses ffmpeg's built-in whisper audio filter (8.0+).
// ffmpeg -i input -vn -af "whisper=model=path:language=en:queue=10:destination=out.json:format=json" -f null -
func (w *Whisper) transcribeFFmpeg(ctx context.Context, audioPath, tmpDir string, opts TranscribeOpts) (*Result, error) {
	model := opts.Model
	if model == "" {
		model = w.defaultModel
	}
	modelPath := resolveWhisperCPPModel(model)

	jsonPath := filepath.Join(tmpDir, "output.json")

	// Build the whisper filter string
	filterParts := []string{
		fmt.Sprintf("model=%s", modelPath),
		"format=json",
		fmt.Sprintf("destination=%s", jsonPath),
		"queue=10",
	}
	lang := opts.Language
	if lang != "" {
		filterParts = append(filterParts, fmt.Sprintf("language=%s", lang))
	}
	filterStr := "whisper=" + strings.Join(filterParts, ":")

	args := []string{
		"-hide_banner", "-loglevel", "warning",
		"-i", audioPath,
		"-vn",
		"-af", filterStr,
		"-f", "null", "-",
	}

	cmd := exec.CommandContext(ctx, w.binary, args...)
	if opts.Verbose {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg whisper filter failed: %w", err)
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read ffmpeg whisper output at %s: %w", jsonPath, err)
	}

	return w.parseFFmpegWhisperOutput(data)
}

// parseFFmpegWhisperOutput parses the JSON output from ffmpeg's whisper filter.
// The ffmpeg whisper filter outputs newline-delimited JSON objects, one per segment.
func (w *Whisper) parseFFmpegWhisperOutput(data []byte) (*Result, error) {
	// ffmpeg whisper JSON: each line is {"from": "00:00:00", "to": "00:00:03", "text": "..."}
	type ffmpegSegment struct {
		From string `json:"from"`
		To   string `json:"to"`
		Text string `json:"text"`
	}

	var segments []Segment
	var fullText strings.Builder

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var seg ffmpegSegment
		if err := json.Unmarshal([]byte(line), &seg); err != nil {
			continue
		}
		start := parseTimestamp(seg.From)
		end := parseTimestamp(seg.To)
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		segments = append(segments, Segment{
			Start: start,
			End:   end,
			Text:  text,
		})
		if fullText.Len() > 0 {
			fullText.WriteString(" ")
		}
		fullText.WriteString(text)
	}

	result := &Result{
		Text:     fullText.String(),
		Segments: segments,
	}
	if len(segments) > 0 {
		result.Duration = segments[len(segments)-1].End
	}
	return result, nil
}

// parseTimestamp converts "HH:MM:SS" or "HH:MM:SS.mmm" to seconds.
func parseTimestamp(ts string) float64 {
	var h, m int
	var s float64
	parts := strings.Split(ts, ":")
	if len(parts) == 3 {
		fmt.Sscanf(parts[0], "%d", &h)
		fmt.Sscanf(parts[1], "%d", &m)
		fmt.Sscanf(parts[2], "%f", &s)
	}
	return float64(h)*3600 + float64(m)*60 + s
}

func isWav(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".wav")
}

func convertToWav(ctx context.Context, audioPath, tmpDir string, verbose bool) (string, error) {
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	wavPath := filepath.Join(tmpDir, base+".wav")
	// Use aresample with first_pts=0 to normalize timestamps; without this,
	// files recorded from PulseAudio may have a large initial PTS offset and
	// ffmpeg pads the output with silence to match, inflating duration.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "warning",
		"-i", audioPath,
		"-af", "aresample=async=1:first_pts=0",
		"-ar", "16000",
		"-ac", "1",
		"-c:a", "pcm_s16le",
		"-y", wavPath,
	)
	if verbose {
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return wavPath, nil
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
// Used by both whisper-cli and ffmpeg whisper filter (both need ggml model files).
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

	// Fall back to model name; the caller will error with a clear message
	return filename
}

func (w *Whisper) findOutputJSON(audioPath, tmpDir string) string {
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	return filepath.Join(tmpDir, base+".json")
}

// whisperOutput is a unified struct that handles JSON from all whisper variants.
// OpenAI whisper/whisperx use "text" + "segments"; whisper-cpp uses "transcription".
type whisperOutput struct {
	// OpenAI whisper / whisperx
	Text     string           `json:"text"`
	Segments []whisperSegment `json:"segments"`
	Language string           `json:"language"`

	// whisper-cpp
	Result        whisperCPPResult    `json:"result"`
	Transcription []whisperCPPSegment `json:"transcription"`
}

type whisperSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type whisperCPPResult struct {
	Language string `json:"language"`
}

type whisperCPPSegment struct {
	Timestamps struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"timestamps"`
	Offsets struct {
		From int `json:"from"`
		To   int `json:"to"`
	} `json:"offsets"`
	Text string `json:"text"`
}

func (w *Whisper) parseOutput(data []byte) (*Result, error) {
	var out whisperOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse whisper JSON: %w", err)
	}

	// whisper-cpp format: "transcription" array with timestamps
	if len(out.Transcription) > 0 {
		return parseWhisperCPPOutput(out), nil
	}

	// OpenAI whisper / whisperx format: "segments" array with start/end floats
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
	// whisperx may omit top-level "text"; rebuild from segments
	if result.Text == "" && len(result.Segments) > 0 {
		var b strings.Builder
		for i, seg := range result.Segments {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(seg.Text)
		}
		result.Text = b.String()
	}
	if len(result.Segments) > 0 {
		result.Duration = result.Segments[len(result.Segments)-1].End
	}
	return result, nil
}

func parseWhisperCPPOutput(out whisperOutput) *Result {
	result := &Result{
		Language: out.Result.Language,
	}
	var fullText strings.Builder
	for _, seg := range out.Transcription {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		// offsets are in milliseconds
		start := float64(seg.Offsets.From) / 1000.0
		end := float64(seg.Offsets.To) / 1000.0
		result.Segments = append(result.Segments, Segment{
			Start: start,
			End:   end,
			Text:  text,
		})
		if fullText.Len() > 0 {
			fullText.WriteString(" ")
		}
		fullText.WriteString(text)
	}
	result.Text = fullText.String()
	if len(result.Segments) > 0 {
		result.Duration = result.Segments[len(result.Segments)-1].End
	}
	return result
}
