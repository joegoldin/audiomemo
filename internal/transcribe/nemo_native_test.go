package transcribe

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
)

// This opt-in check uses the real runtime and cached models, never cloud APIs.
func TestNemoNative(t *testing.T) {
	binary := os.Getenv("AUDIOMEMO_NEMO_TEST_BINARY")
	if binary == "" {
		t.Skip("set AUDIOMEMO_NEMO_TEST_BINARY with models pre-downloaded to run native inference")
	}
	cfg := config.Default()
	cfg.Transcribe.Nemo.Binary = binary
	if device := os.Getenv("AUDIOMEMO_NEMO_TEST_DEVICE"); device != "" {
		cfg.Transcribe.Nemo.Device = device
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	audio := filepath.Join("..", "..", "testdata", "test.ogg")
	t.Run("offline", func(t *testing.T) {
		result, err := NewNemo(cfg.Transcribe.Nemo).Transcribe(ctx, audio, TranscribeOpts{Diarize: true})
		if err != nil {
			t.Fatal(err)
		}
		if result.Text == "" || len(result.Words) == 0 || len(result.SpeakerTurns) == 0 {
			t.Fatalf("incomplete result: %+v", result)
		}
		if !strings.Contains(result.Format(FormatText), "speaker_") {
			t.Fatal("no speaker-attributed transcript")
		}
		t.Logf("Native final: %d words, %d speaker turns, %.1fs audio", len(result.Words), len(result.SpeakerTurns), result.Duration)
	})
	t.Run("live", func(t *testing.T) {
		pcm, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", audio, "-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:1").Output()
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewLiveStreamer(cfg, "nemo")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Start(ctx, bytes.NewReader(pcm), filepath.Join(t.TempDir(), "preview.txt")); err != nil {
			t.Fatal(err)
		}
		defer s.Stop()
		select {
		case <-s.done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		s.Stop()
		for err := range s.Err {
			t.Errorf("live error: %v", err)
		}
		for warning := range s.Warning {
			t.Errorf("live warning: %v", warning)
		}
		if strings.TrimSpace(s.FullText()) == "" {
			t.Fatal("no native live transcript")
		}
		t.Logf("Native live preview: %d characters", len(s.FullText()))
	})
}
