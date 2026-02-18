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
	if !found["-np"] {
		t.Errorf("expected -np in args: %v", args)
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
