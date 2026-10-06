package transcribe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joegoldin/audiomemo/internal/config"
)

func TestLocalLiveProtocol(t *testing.T) {
	audio := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions/realtime" || r.Header.Get("Authorization") != "Bearer test-local-token" {
			t.Errorf("incorrect local endpoint/auth")
			return
		}
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var update struct {
			Type    string `json:"type"`
			Session struct {
				SampleRate int `json:"sample_rate"`
			} `json:"session"`
		}
		if err := conn.ReadJSON(&update); err != nil {
			t.Error(err)
			return
		}
		if update.Type != "session.update" || update.Session.SampleRate != 16000 {
			t.Errorf("session: %+v", update)
		}
		var received []byte
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if kind == websocket.BinaryMessage {
				received = append(received, data...)
				continue
			}
			if !strings.Contains(string(data), "input_audio_buffer.commit") {
				t.Errorf("unexpected message %s", data)
			}
			audio <- received
			conn.WriteJSON(map[string]string{"type": "conversation.item.input_audio_transcription.delta", "delta": "Hello"})
			conn.WriteJSON(map[string]string{"type": "conversation.item.input_audio_transcription.delta", "delta": " world"})
			conn.WriteJSON(map[string]string{"type": "conversation.item.input_audio_transcription.completed", "transcript": "Hello world."})
			conn.WriteJSON(map[string]string{"type": "input_audio_buffer.committed"})
			return
		}
	}))
	defer server.Close()
	cfg := config.Default()
	s, err := NewLiveStreamer(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	var cleaned atomic.Bool
	s.startLocal = func(context.Context, config.NemoConfig) (string, string, func(), error) {
		return "ws://" + server.Listener.Addr().String() + "/v1/audio/transcriptions/realtime", "test-local-token", func() { cleaned.Store(true) }, nil
	}
	file := filepath.Join(t.TempDir(), "preview.txt")
	if err := s.Start(t.Context(), strings.NewReader("pcm-data"), file); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if text, ok := waitChan(s.Committed, time.Second*3); !ok || text != "Hello world." {
		t.Fatalf("committed %q %v", text, ok)
	}
	s.Stop()
	if !cleaned.Load() {
		t.Fatal("local process not stopped")
	}
	if string(<-audio) != "pcm-data" {
		t.Fatal("audio changed")
	}
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), "Hello world.") {
		t.Fatalf("file: %s", data)
	}
	partials := []string{}
	for partial := range s.Partial {
		partials = append(partials, partial)
	}
	if len(partials) < 2 || partials[1] != "Hello world" {
		t.Fatalf("deltas not accumulated: %v", partials)
	}
	for err := range s.Err {
		t.Errorf("unexpected stream error: %v", err)
	}
}

func TestLiveLocalFailureFallsBackToCloud(t *testing.T) {
	for _, midstream := range []bool{false, true} {
		t.Run(map[bool]string{false: "startup", true: "midstream"}[midstream], func(t *testing.T) {
			var cloudCalls atomic.Int32
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cloudCalls.Add(1)
				if r.URL.Query().Get("model_id") != "scribe_v2_realtime" {
					t.Errorf("wrong realtime model")
				}
				if r.URL.Query().Get("enable_logging") != "false" {
					t.Error("cloud fallback must preserve upstream's opt-out of logging")
				}
				conn, err := wsUpgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				conn.WriteJSON(map[string]string{"message_type": "committed_transcript", "text": "Cloud preview."})
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			}))
			defer cloud.Close()
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := wsUpgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				conn.WriteJSON(map[string]any{"type": "error", "error": map[string]string{"message": "inference failed"}})
			}))
			defer local.Close()
			cfg := config.Default()
			cfg.Transcribe.ElevenLabs.APIKey = "test"
			s, _ := NewLiveStreamer(cfg, "")
			s.baseURL = "ws://" + cloud.Listener.Addr().String()
			s.startLocal = func(context.Context, config.NemoConfig) (string, string, func(), error) {
				if !midstream {
					return "", "", nil, errors.New("missing model")
				}
				return "ws://" + local.Listener.Addr().String(), "key", func() {}, nil
			}
			if err := s.Start(t.Context(), strings.NewReader("pcm-data"), filepath.Join(t.TempDir(), "preview")); err != nil {
				t.Fatal(err)
			}
			defer s.Stop()
			if text, ok := waitChan(s.Committed, 3*time.Second); !ok || text != "Cloud preview." {
				t.Fatalf("fallback text: %q %v", text, ok)
			}
			if cloudCalls.Load() != 1 {
				t.Fatal("cloud not reached exactly once")
			}
			if warning, ok := waitErr(s.Warning, time.Second); !ok || !strings.Contains(warning.Error(), "uploaded") {
				t.Fatalf("missing upload notice: %v", warning)
			}
		})
	}
}

func TestLiveStrictLocalAndNoKeyNeverFallback(t *testing.T) {
	for _, backend := range []string{"nemo", "auto"} {
		cfg := config.Default()
		if backend == "nemo" {
			cfg.Transcribe.ElevenLabs.APIKey = "test"
		}
		s, _ := NewLiveStreamer(cfg, backend)
		s.baseURL = "ws://127.0.0.1:1"
		s.startLocal = func(context.Context, config.NemoConfig) (string, string, func(), error) {
			return "", "", nil, errors.New("unavailable")
		}
		pr, pw := io.Pipe()
		if err := s.Start(t.Context(), pr, filepath.Join(t.TempDir(), "preview")); err != nil {
			t.Fatal(err)
		}
		if err, ok := waitErr(s.Err, time.Second); !ok || !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("error: %v", err)
		}
		// A failed preview must still drain the recorder's pipe, even after the
		// bounded startup queue fills. Otherwise recording itself deadlocks.
		written := make(chan error, 1)
		go func() { _, err := pw.Write(make([]byte, 2<<20)); pw.Close(); written <- err }()
		select {
		case err := <-written:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("preview blocked recording")
		}
		s.Stop()
	}
}

func TestLiveBackendSelection(t *testing.T) {
	cfg := config.Default()
	cfg.Transcribe.ElevenLabs.APIKey = "test"
	for _, backend := range []string{"deepgram", "whisper", "whisper-cpp", "openai"} {
		s, err := NewLiveStreamer(cfg, backend)
		if err != nil || s != nil {
			t.Fatalf("%s should be batch only", backend)
		}
	}
	cfg.Transcribe.DefaultBackend = "nemo"
	s, err := NewLiveStreamer(cfg, "")
	if err != nil || s.fallback {
		t.Fatal("explicit local config enabled cloud")
	}
}

func TestManagedLiveStopCancelsStartup(t *testing.T) {
	s, _ := NewLiveStreamer(config.Default(), "")
	stopped := make(chan struct{})
	s.startLocal = func(ctx context.Context, cfg config.NemoConfig) (string, string, func(), error) {
		<-ctx.Done()
		close(stopped)
		return "", "", nil, ctx.Err()
	}
	pr, pw := io.Pipe()
	defer pw.Close()
	if err := s.Start(t.Context(), pr, filepath.Join(t.TempDir(), "preview")); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	select {
	case <-stopped:
	default:
		t.Fatal("startup not canceled")
	}
}
