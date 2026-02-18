package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := Default()
	if cfg.Record.Format != "ogg" {
		t.Errorf("expected default format ogg, got %s", cfg.Record.Format)
	}
	if cfg.Record.SampleRate != 48000 {
		t.Errorf("expected default sample rate 48000, got %d", cfg.Record.SampleRate)
	}
	if cfg.Record.Channels != 1 {
		t.Errorf("expected default channels 1, got %d", cfg.Record.Channels)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`
[record]
format = "wav"
sample_rate = 44100

[transcribe]
default_backend = "deepgram"

[transcribe.deepgram]
api_key = "test-key"
model = "nova-2"
`), 0644)

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Record.Format != "wav" {
		t.Errorf("expected wav, got %s", cfg.Record.Format)
	}
	if cfg.Record.SampleRate != 44100 {
		t.Errorf("expected 44100, got %d", cfg.Record.SampleRate)
	}
	if cfg.Record.Channels != 1 {
		t.Errorf("expected default channels 1, got %d", cfg.Record.Channels)
	}
	if cfg.Transcribe.DefaultBackend != "deepgram" {
		t.Errorf("expected deepgram, got %s", cfg.Transcribe.DefaultBackend)
	}
	if cfg.Transcribe.Deepgram.APIKey != "test-key" {
		t.Errorf("expected test-key, got %s", cfg.Transcribe.Deepgram.APIKey)
	}
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	cfg, err := LoadFrom("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Record.Format != "ogg" {
		t.Errorf("expected default ogg, got %s", cfg.Record.Format)
	}
}

func TestEnvVarOverridesConfig(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "env-key")
	cfg := Default()
	cfg.ApplyEnv()
	if cfg.Transcribe.Deepgram.APIKey != "env-key" {
		t.Errorf("expected env-key, got %s", cfg.Transcribe.Deepgram.APIKey)
	}
}

func TestResolveOutputDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg := Default()
	dir := cfg.ResolveOutputDir()
	expected := filepath.Join(home, "Recordings")
	if dir != expected {
		t.Errorf("expected %s, got %s", expected, dir)
	}
}
