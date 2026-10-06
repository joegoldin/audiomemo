package transcribe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joegoldin/audiomemo/internal/config"
)

func TestDispatcherDefaultsLocalFirst(t *testing.T) {
	for _, name := range []string{"", "auto"} {
		cfg := config.Default()
		cfg.Transcribe.DefaultBackend = name
		cfg.Transcribe.Deepgram.APIKey = "dg"
		cfg.Transcribe.OpenAI.APIKey = "oai"
		cfg.Transcribe.ElevenLabs.APIKey = "el"
		tr, err := NewDispatcher(cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		auto, ok := tr.(*localFirst)
		if !ok || auto.local.Name() != "nemo" || auto.fallback.Name() != "elevenlabs" {
			t.Fatalf("dispatcher: %#v", tr)
		}
	}
}

func TestDispatcherExplicitBackend(t *testing.T) {
	cfg := config.Default()
	if _, err := NewDispatcher(cfg, "deepgram"); err == nil {
		t.Fatal("expected missing key error")
	}
	cfg.Transcribe.Deepgram.APIKey = "key"
	cfg.Transcribe.ElevenLabs.APIKey = "key"
	for _, name := range []string{"nemo", "local", "elevenlabs", "deepgram"} {
		tr, err := NewDispatcher(cfg, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := tr.(*localFirst); ok {
			t.Fatalf("explicit %s must not fall back", name)
		}
	}
	cfg.Transcribe.DefaultBackend = "elevenlabs"
	tr, err := NewDispatcher(cfg, "")
	if err != nil || tr.Name() != "elevenlabs" {
		t.Fatalf("explicit config: %v, %v", tr, err)
	}
	if _, err := NewDispatcher(cfg, "unknown"); err == nil {
		t.Fatal("expected unknown backend error")
	}
}

type stubTranscriber struct {
	calls int
	opts  TranscribeOpts
	err   error
}

func (s *stubTranscriber) Name() string { return "stub" }
func (s *stubTranscriber) Transcribe(ctx context.Context, path string, opts TranscribeOpts) (*Result, error) {
	s.calls++
	s.opts = opts
	return &Result{Text: "test"}, s.err
}

func TestLocalFirstFallbackPolicy(t *testing.T) {
	file := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(file, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name                                        string
		localErr                                    error
		canceled, fallbackError, noKey, invalidOpts bool
		wantCalls                                   int
		wantErr                                     bool
	}{
		{name: "local success"},
		{name: "local failure", localErr: errors.New("missing model"), wantCalls: 1},
		{name: "no key", localErr: errors.New("missing binary"), noKey: true, wantErr: true},
		{name: "both fail", localErr: errors.New("local failure"), fallbackError: true, wantCalls: 1, wantErr: true},
		{name: "canceled", localErr: context.Canceled, wantErr: true},
		{name: "deadline", localErr: context.DeadlineExceeded, wantErr: true},
		{name: "context canceled", localErr: errors.New("killed"), canceled: true, wantErr: true},
		{name: "invalid opts", invalidOpts: true, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			local := &stubTranscriber{err: tt.localErr}
			cloud := &stubTranscriber{}
			if tt.fallbackError {
				cloud.err = errors.New("cloud failure")
			}
			f := &localFirst{local: local, fallback: cloud}
			if tt.noKey {
				f.fallback = nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			_, err := f.Transcribe(ctx, file, TranscribeOpts{Model: "/model.gguf", Diarize: true, SmartFormat: tt.invalidOpts})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error: %v", err)
			}
			if cloud.calls != tt.wantCalls {
				t.Fatalf("cloud calls %d want %d", cloud.calls, tt.wantCalls)
			}
			if cloud.calls > 0 && (cloud.opts.Model != "" || !cloud.opts.Diarize) {
				t.Fatalf("fallback opts: %+v", cloud.opts)
			}
			if tt.fallbackError && (!strings.Contains(err.Error(), "local failure") || !strings.Contains(err.Error(), "cloud failure")) {
				t.Fatalf("missing causes: %v", err)
			}
		})
	}
}

func TestLocalFirstMissingFileNeverUploads(t *testing.T) {
	cloud := &stubTranscriber{}
	f := &localFirst{local: &stubTranscriber{}, fallback: cloud}
	if _, err := f.Transcribe(t.Context(), "/nonexistent/audio", TranscribeOpts{}); err == nil || cloud.calls != 0 {
		t.Fatal("missing file must not reach cloud")
	}
}
