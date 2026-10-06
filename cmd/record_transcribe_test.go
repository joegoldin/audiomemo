package cmd

import (
	"github.com/joegoldin/audiomemo/internal/config"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPostTranscribePreservesConfigAndOverrides(t *testing.T) {
	oldConfig, oldArgs, oldVerbose := rConfig, rTranscribeArgs, rVerbose
	t.Cleanup(func() { rConfig, rTranscribeArgs, rVerbose = oldConfig, oldArgs, oldVerbose })
	rConfig = "/config/record.toml"
	rTranscribeArgs = "--backend nemo --diarize=false --config /config/final.toml"
	rVerbose = true
	want := []string{"--config", "/config/record.toml", "--verbose", "--backend", "nemo", "--diarize=false", "--config", "/config/final.toml", "/audio/file.ogg"}
	_, got, err := newPostTranscribeCmd("/audio/file.ogg", false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args: %v", got)
	}
}

func TestPreviewHonorsFinalBackendSelection(t *testing.T) {
	oldArgs := rTranscribeArgs
	t.Cleanup(func() { rTranscribeArgs = oldArgs })
	cfg := config.Default()
	cfg.Transcribe.DefaultBackend = "elevenlabs"
	cfg.Transcribe.ElevenLabs.APIKey = "test-key"
	for _, args := range []string{
		"--backend nemo", "--backend=nemo", "-b nemo", "-bnemo",
		"--diarize --backend nemo", "--model parakeet-tdt --backend nemo",
		"--backend elevenlabs --backend nemo",
	} {
		t.Run(args, func(t *testing.T) {
			rTranscribeArgs = args
			s, _, err := recordLiveStreamer(cfg, false)
			if err != nil {
				t.Fatal(err)
			}
			if s == nil || s.Name() != "nemo" {
				t.Fatal("preview did not select local backend")
			}
		})
	}
	rTranscribeArgs = "--backend whisper-cpp"
	if s, note, err := recordLiveStreamer(cfg, false); err != nil || s != nil || note == "" {
		t.Fatalf("batch-only preview: %v %q %v", s, note, err)
	}
	rTranscribeArgs = ""
	if s, _, err := recordLiveStreamer(cfg, true); err != nil || s != nil {
		t.Fatal("disabled/recw preview must not start")
	}
}

func TestPreviewHonorsFinalConfigOverride(t *testing.T) {
	oldArgs := rTranscribeArgs
	t.Cleanup(func() { rTranscribeArgs = oldArgs })
	cfg := config.Default()
	cfg.Transcribe.DefaultBackend = "nemo"
	path := filepath.Join(t.TempDir(), "local.toml")
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	rTranscribeArgs = "--config " + path
	cfg.Transcribe.DefaultBackend = "elevenlabs"
	cfg.Transcribe.ElevenLabs.APIKey = "test-key"
	s, _, err := recordLiveStreamer(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.Name() != "nemo" {
		t.Fatal("preview ignored final config's local-only backend")
	}
}
