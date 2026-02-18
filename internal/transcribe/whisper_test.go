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

func TestWhisperBinaryNotFound(t *testing.T) {
	w := NewWhisper("nonexistent-binary-xyz", "base")
	_, err := w.Transcribe(context.Background(), "test.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error when binary not found")
	}
}

func TestWhisperFileNotFound(t *testing.T) {
	// Only run if whisper is actually available
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
	args := w.buildArgs("/tmp/test.wav", TranscribeOpts{
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
	if !found["--output-format"] || !found["json"] {
		t.Errorf("expected --output-format json in args: %v", args)
	}
}
