package transcribe

import (
	"os/exec"
	"testing"

	"github.com/joegoldin/audiomemo/internal/config"
)

func TestAutoDetectWithExplicitBackend(t *testing.T) {
	cfg := config.Default()
	tr, err := NewDispatcher(cfg, "deepgram")
	if err == nil && tr != nil {
		// Should fail because no API key
	}
	// With key set:
	cfg.Transcribe.Deepgram.APIKey = "test"
	tr, err = NewDispatcher(cfg, "deepgram")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Name() != "deepgram" {
		t.Errorf("expected deepgram, got %s", tr.Name())
	}
}

func TestAutoDetectWithConfigDefault(t *testing.T) {
	cfg := config.Default()
	cfg.Transcribe.DefaultBackend = "openai"
	cfg.Transcribe.OpenAI.APIKey = "test"
	tr, err := NewDispatcher(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Name() != "openai" {
		t.Errorf("expected openai, got %s", tr.Name())
	}
}

func TestAutoDetectScansKeys(t *testing.T) {
	cfg := config.Default()
	cfg.Transcribe.Mistral.APIKey = "test"
	tr, err := NewDispatcher(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Name() != "mistral" {
		t.Errorf("expected mistral, got %s", tr.Name())
	}
}

func TestAutoDetectPriorityOrder(t *testing.T) {
	cfg := config.Default()
	cfg.Transcribe.Deepgram.APIKey = "dg"
	cfg.Transcribe.OpenAI.APIKey = "oai"
	cfg.Transcribe.Mistral.APIKey = "mis"
	tr, err := NewDispatcher(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	// Deepgram should win (first in scan order)
	if tr.Name() != "deepgram" {
		t.Errorf("expected deepgram (highest priority), got %s", tr.Name())
	}
}

func TestAutoDetectNoBackendAvailable(t *testing.T) {
	// Skip if any whisper binary is on PATH (e.g. in nix dev shell)
	for _, name := range []string{"whisper-cli", "whisper", "whisperx"} {
		if _, err := exec.LookPath(name); err == nil {
			t.Skipf("%s is on PATH, auto-detect will find it", name)
		}
	}
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		if ffmpegHasWhisperFilter(path) {
			t.Skip("ffmpeg has whisper filter, auto-detect will find it")
		}
	}
	cfg := config.Default()
	_, err := NewDispatcher(cfg, "")
	if err == nil {
		t.Error("expected error when no backend available")
	}
}
