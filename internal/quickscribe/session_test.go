package quickscribe

import (
	"bytes"
	"context"
	"errors"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"io"
	"testing"
	"time"
)

func TestMicNeverUsesMonitorOrAmbiguousGroup(t *testing.T) {
	c := config.Default()
	ds := []record.Device{{Name: "a", IsDefault: true}, {Name: "b"}, {Name: "audio.monitor", IsMonitor: true}}
	c.Devices = map[string]string{"mic": "a", "mic2": "b", "remote": "audio.monitor"}
	c.DeviceGroups["call"] = []string{"mic", "remote"}
	c.Record.Device = "call"
	if d, e := mic(c, "", ds); e != nil || d != "a" {
		t.Fatal(d, e)
	}
	if _, e := mic(c, "call", ds); e == nil {
		t.Fatal("accepted whole group")
	}
	if _, e := mic(c, "remote", ds); e == nil {
		t.Fatal("accepted monitor")
	}
	c.SetDeviceRole("mic2", "us")
	if _, e := mic(c, "", ds); e == nil {
		t.Fatal("guessed ambiguous mic")
	}
}
func TestPrivateClipIncludesStartAndFinalizesOnce(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		ctx := t.Context()
		r, w := io.Pipe()
		done := make(chan string, 1)
		var calls int
		final := func(ctx context.Context, pcm []byte) (string, error) {
			calls++
			if !bytes.Equal(pcm, []byte{1, 2, 3, 4}) {
				t.Errorf("wrong retained PCM length: %d", len(pcm))
			}
			return "hello", nil
		}
		s := newSession(ctx, r, func(context.Context) error { return w.Close() }, final, func(reason string) { done <- reason }, 4)
		go func() { _, _ = w.Write([]byte{1, 2, 3, 4, 5, 6}) }()
		_, e := io.Copy(io.Discard, s.feed)
		if e != nil {
			t.Fatal(e)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("limit did not finish")
		}
		if cancel {
			s.Cancel()
			if _, e = s.Stop(ctx); !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			if calls != 0 {
				t.Fatal(calls)
			}
		} else {
			for range 2 {
				v, e := s.Stop(ctx)
				if e != nil || v != "hello" {
					t.Fatal(v, e)
				}
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		}
	}
}
func TestLocalFailureCannotDialCloud(t *testing.T) {
	c := config.Default()
	c.Transcribe.Nemo.Binary = "/missing/nemo"
	c.Transcribe.ElevenLabs.APIKey = "not-a-real-key"
	c.Transcribe.DefaultBackend = "elevenlabs"
	_, e := Start(t.Context(), c, "id", "", func(sessiontypes.Preview) {}, func(string) {})
	if e == nil {
		t.Fatal("missing local backend must fail before opening mic")
	}
}

func TestFailedFinalDoesNotReturnPartialText(t *testing.T) {
	fail := errors.New("model failed")
	src := io.NopCloser(bytes.NewReader([]byte{7, 8}))
	s := newSession(t.Context(), src, func(context.Context) error { return nil }, func(context.Context, []byte) (string, error) { return "prefix", fail }, nil, 100)
	if _, err := io.Copy(io.Discard, s.feed); err != nil {
		t.Fatal(err)
	}
	text, err := s.Stop(t.Context())
	if text != "" || !errors.Is(err, fail) {
		t.Fatalf("returned text=%t error=%v", text != "", err)
	}
}
