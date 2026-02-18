package transcribe

import (
	"context"
	"os/exec"
	"testing"
)

func TestWhisperName(t *testing.T) {
	w := NewWhisper("whisper", "base")
	if w.Name() != "whisper" {
		t.Errorf("expected 'whisper', got %s", w.Name())
	}
}

func TestWhisperCPPName(t *testing.T) {
	w := NewWhisper("whisper-cli", "base")
	if w.Name() != "whisper-cpp" {
		t.Errorf("expected 'whisper-cpp', got %s", w.Name())
	}
}

func TestWhisperXName(t *testing.T) {
	w := NewWhisper("whisperx", "base")
	if w.Name() != "whisperx" {
		t.Errorf("expected 'whisperx', got %s", w.Name())
	}
}

func TestWhisperBinaryNotFound(t *testing.T) {
	w := NewWhisper("nonexistent-binary-xyz", "base")
	_, err := w.Transcribe(context.Background(), "test.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error when binary not found")
	}
}

func TestWhisperFileNotFound(t *testing.T) {
	if _, err := exec.LookPath("whisper"); err != nil {
		t.Skip("whisper not on PATH")
	}
	w := NewWhisper("whisper", "base")
	_, err := w.Transcribe(context.Background(), "/nonexistent/file.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestWhisperBuildArgs(t *testing.T) {
	w := NewWhisper("whisper", "base")
	args := w.buildArgs("/tmp/test.wav", "/tmp/out", TranscribeOpts{
		Model:    "large-v3",
		Language: "en",
	})
	found := map[string]bool{}
	for _, a := range args {
		found[a] = true
	}
	if !found["--model"] || !found["large-v3"] {
		t.Errorf("expected --model large-v3 in args: %v", args)
	}
	if !found["--language"] || !found["en"] {
		t.Errorf("expected --language en in args: %v", args)
	}
	if !found["--output_format"] || !found["json"] {
		t.Errorf("expected --output_format json in args: %v", args)
	}
}

func TestWhisperCPPBuildArgs(t *testing.T) {
	w := NewWhisper("whisper-cli", "base")
	args := w.buildArgs("/tmp/test.wav", "/tmp/out", TranscribeOpts{
		Model:    "base",
		Language: "en",
	})
	found := map[string]bool{}
	for _, a := range args {
		found[a] = true
	}
	if !found["-oj"] {
		t.Errorf("expected -oj in args: %v", args)
	}
	if !found["-l"] || !found["en"] {
		t.Errorf("expected -l en in args: %v", args)
	}
	if !found["-f"] || !found["/tmp/test.wav"] {
		t.Errorf("expected -f /tmp/test.wav in args: %v", args)
	}
}

func TestWhisperXBuildArgs(t *testing.T) {
	w := NewWhisper("whisperx", "base")
	args := w.buildArgs("/tmp/test.wav", "/tmp/out", TranscribeOpts{
		Model:    "large-v3",
		Language: "fr",
	})
	found := map[string]bool{}
	for _, a := range args {
		found[a] = true
	}
	if !found["--model"] || !found["large-v3"] {
		t.Errorf("expected --model large-v3 in args: %v", args)
	}
	if !found["--language"] || !found["fr"] {
		t.Errorf("expected --language fr in args: %v", args)
	}
	if !found["--output_format"] || !found["json"] {
		t.Errorf("expected --output_format json in args: %v", args)
	}
}

func TestDetectVariant(t *testing.T) {
	tests := []struct {
		binary  string
		variant whisperVariant
	}{
		{"whisper", variantWhisper},
		{"whisper-cli", variantWhisperCPP},
		{"/nix/store/xyz/bin/whisper-cli", variantWhisperCPP},
		{"whisperx", variantWhisperX},
		{"/usr/bin/whisperx", variantWhisperX},
	}
	for _, tt := range tests {
		v := detectVariant(tt.binary)
		if v != tt.variant {
			t.Errorf("detectVariant(%q) = %d, want %d", tt.binary, v, tt.variant)
		}
	}
}

func TestFFmpegWhisperName(t *testing.T) {
	w := &Whisper{binary: "ffmpeg", variant: variantFFmpegWhisper, defaultModel: "base"}
	if w.Name() != "ffmpeg-whisper" {
		t.Errorf("expected 'ffmpeg-whisper', got %s", w.Name())
	}
}

func TestDetectVariantFFmpeg(t *testing.T) {
	v := detectVariant("/usr/bin/ffmpeg")
	if v != variantFFmpegWhisper {
		t.Errorf("detectVariant(ffmpeg) = %d, want %d", v, variantFFmpegWhisper)
	}
}

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		ts       string
		expected float64
	}{
		{"00:00:00", 0},
		{"00:00:03", 3},
		{"00:01:30", 90},
		{"01:00:00", 3600},
		{"00:00:03.500", 3.5},
		{"01:02:03.456", 3723.456},
	}
	for _, tt := range tests {
		got := parseTimestamp(tt.ts)
		if got < tt.expected-0.001 || got > tt.expected+0.001 {
			t.Errorf("parseTimestamp(%q) = %f, want %f", tt.ts, got, tt.expected)
		}
	}
}

func TestParseFFmpegWhisperOutput(t *testing.T) {
	w := &Whisper{variant: variantFFmpegWhisper}
	data := []byte(`{"from": "00:00:00", "to": "00:00:03", "text": "Hello world"}
{"from": "00:00:03", "to": "00:00:06.500", "text": "How are you"}
`)
	result, err := w.parseFFmpegWhisperOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(result.Segments))
	}
	if result.Segments[0].Text != "Hello world" {
		t.Errorf("segment 0 text = %q, want 'Hello world'", result.Segments[0].Text)
	}
	if result.Segments[1].End < 6.499 || result.Segments[1].End > 6.501 {
		t.Errorf("segment 1 end = %f, want 6.5", result.Segments[1].End)
	}
	if result.Text != "Hello world How are you" {
		t.Errorf("full text = %q", result.Text)
	}
	if result.Duration < 6.499 || result.Duration > 6.501 {
		t.Errorf("duration = %f, want 6.5", result.Duration)
	}
}

func TestParseFFmpegWhisperOutputEmpty(t *testing.T) {
	w := &Whisper{variant: variantFFmpegWhisper}
	result, err := w.parseFFmpegWhisperOutput([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "" {
		t.Errorf("expected empty text, got %q", result.Text)
	}
	if len(result.Segments) != 0 {
		t.Errorf("expected 0 segments, got %d", len(result.Segments))
	}
}

func TestResolveWhisperCPPModelPath(t *testing.T) {
	// Direct path should pass through
	p := resolveWhisperCPPModel("/some/path/ggml-base.bin")
	if p != "/some/path/ggml-base.bin" {
		t.Errorf("expected path passthrough, got %s", p)
	}

	// .bin suffix should pass through
	p = resolveWhisperCPPModel("custom-model.bin")
	if p != "custom-model.bin" {
		t.Errorf("expected .bin passthrough, got %s", p)
	}

	// Model name without path should produce ggml-NAME.bin fallback
	p = resolveWhisperCPPModel("base")
	if p == "" {
		t.Error("expected non-empty result for model name")
	}
}
