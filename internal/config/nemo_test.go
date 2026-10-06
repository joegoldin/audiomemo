package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalTranscriptionDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Transcribe.DefaultBackend != "auto" {
		t.Fatal("default must be local-first auto")
	}
	n := cfg.Transcribe.Nemo
	if n.Binary != "nemo-speech" || n.Model != "parakeet-tdt" || n.LiveModel != "nemotron-en" || n.DiarModel != "nemotron-3-diarization" || !n.Diarize {
		t.Fatalf("defaults: %+v", n)
	}
}

func TestNemoConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`[transcribe]
default_backend = "nemo"
[transcribe.nemo]
binary = "/opt/nemo/bin/nemo-speech"
device = "vulkan:0"
diarize = false
startup_timeout = 45
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Transcribe.Nemo.Diarize || cfg.Transcribe.Nemo.Model != "parakeet-tdt" || cfg.Transcribe.Nemo.Device != "vulkan:0" {
		t.Fatalf("config: %+v", cfg.Transcribe.Nemo)
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Transcribe.Nemo != cfg.Transcribe.Nemo {
		t.Fatal("NeMo config changed on round trip")
	}
}
