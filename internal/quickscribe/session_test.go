package quickscribe

import (
	"bytes"
	"context"
	"errors"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"io"
	"slices"
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
	_, e := Start(t.Context(), c, "id", "", false, func(sessiontypes.Preview) {}, func(string) {})
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

func TestQuickGroupPreservesOnlyTwoSides(t *testing.T) {
	c := config.Default()
	c.Desktop.DictationMic = "local"
	c.Record.Device = "local"
	c.Devices = map[string]string{"mic": "local", "speaker": "remote.monitor", "monitor": "other.monitor"}
	c.DeviceGroups["call"] = []string{"speaker", "mic", "monitor"}
	c.DeviceGroups["remote"] = []string{"speaker", "monitor"}
	ds := []record.Device{{Name: "local", IsDefault: true}, {Name: "remote.monitor", IsMonitor: true}, {Name: "other.monitor", IsMonitor: true}}
	devices, roles, err := quickDevices(c, "call", false, ds)
	if err != nil || !slices.Equal(devices, []string{"remote.monitor", "local", "other.monitor"}) || !slices.Equal(roles, []string{"them", "us", "them"}) {
		t.Fatalf("mixed group: %v %v %v", devices, roles, err)
	}
	if devices, roles, err := quickDevices(c, "remote", false, ds); err != nil || len(devices) != 2 || roles != nil {
		t.Fatalf("one-sided group: %v %v %v", devices, roles, err)
	}
	if devices, roles, err := quickDevices(c, "speaker", false, ds); err != nil || !slices.Equal(devices, []string{"remote.monitor"}) || roles != nil {
		t.Fatalf("explicit system audio: %v %v %v", devices, roles, err)
	}
	if devices, roles, err := quickDevices(c, "", false, ds); err != nil || !slices.Equal(devices, []string{"local"}) || roles != nil {
		t.Fatalf("implicit microphone: %v %v %v", devices, roles, err)
	}
}

func TestBothRequiresConfiguredTwoSidedGroup(t *testing.T) {
	c := config.Default()
	c.Desktop.DictationMic = "mic"
	c.Devices = map[string]string{"mic": "mic", "playback": "audio.monitor"}
	c.DeviceGroups["call"] = []string{"mic", "playback"}
	c.DeviceGroups["local-only"] = []string{"mic"}
	ds := []record.Device{{Name: "mic", IsDefault: true}, {Name: "audio.monitor", IsMonitor: true}}
	if _, _, err := quickDevices(c, "", true, ds); err == nil {
		t.Fatal("selected system audio without a configured group")
	}
	c.Desktop.MeetingDevice = "call"
	devices, roles, err := quickDevices(c, "", true, ds)
	if err != nil || !slices.Equal(devices, []string{"mic", "audio.monitor"}) || !slices.Equal(roles, []string{"us", "them"}) {
		t.Fatalf("meeting group fallback: %v %v %v", devices, roles, err)
	}
	c.Desktop.DictationGroup = "local-only"
	if _, _, err := quickDevices(c, "", true, ds); err == nil {
		t.Fatal("accepted a one-sided override")
	}
	c.Desktop.DictationGroup = "call"
	if _, roles, err := quickDevices(c, "", true, ds); err != nil || len(roles) != 2 {
		t.Fatalf("two-sided override: %v %v", roles, err)
	}
	c.Record.Device = "call"
	for _, mic := range []string{"mic", ""} {
		c.Desktop.DictationMic = mic
		if devices, roles, err := quickDevices(c, "", false, ds); err != nil || !slices.Equal(devices, []string{"mic"}) || roles != nil {
			t.Fatalf("regular dictation opened both sides: %v %v %v", devices, roles, err)
		}
	}
}
