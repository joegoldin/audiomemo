package stream

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

// decodeLines splits NDJSON output into one generic map per line. Tests assert
// on the map rather than on a struct so a field silently disappearing from the
// wire shows up as a failure.
func decodeLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %q is not JSON: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// fakeClock advances only when a test says so, so `t` is deterministic.
type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time { return c.at }

func newTestEmitter() (*Emitter, *bytes.Buffer, *fakeClock) {
	buf := &bytes.Buffer{}
	clk := &fakeClock{at: time.Unix(1000, 0)}
	return newEmitter(buf, clk.now), buf, clk
}

func TestStartEvent(t *testing.T) {
	em, buf, _ := newTestEmitter()
	em.Start(StartEvent{
		Device:      "alsa_input.usb-Blue_Yeti-00.analog-stereo",
		DeviceLabel: "mic",
		Devices:     []string{"alsa_input.usb-Blue_Yeti-00.analog-stereo"},
		Path:        "/tmp/memo.ogg",
		Format:      "ogg",
		SampleRate:  48000,
		Channels:    1,
		Mode:        ModeLive,
		Backend:     "elevenlabs",
	})
	lines := decodeLines(t, buf.String())
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	got := lines[0]
	if got["type"] != "start" {
		t.Errorf("type = %v, want start", got["type"])
	}
	if got["device_label"] != "mic" {
		t.Errorf("device_label = %v", got["device_label"])
	}
	if got["path"] != "/tmp/memo.ogg" {
		t.Errorf("path = %v", got["path"])
	}
	if got["mode"] != "live" {
		t.Errorf("mode = %v, want live", got["mode"])
	}
	if got["sample_rate"] != float64(48000) {
		t.Errorf("sample_rate = %v", got["sample_rate"])
	}
	if got["t"] != float64(0) {
		t.Errorf("t = %v, want 0 on the first event", got["t"])
	}
}

func TestLevelEventCarriesBothScales(t *testing.T) {
	em, buf, clk := newTestEmitter()
	clk.at = clk.at.Add(250 * time.Millisecond)
	em.Level(0.35, -39.0)
	got := decodeLines(t, buf.String())[0]
	if got["type"] != "level" {
		t.Errorf("type = %v", got["type"])
	}
	if got["rms"] != 0.35 {
		t.Errorf("rms = %v, want 0.35", got["rms"])
	}
	if got["db"] != -39.0 {
		t.Errorf("db = %v, want -39", got["db"])
	}
	if got["t"] != float64(250) {
		t.Errorf("t = %v, want 250", got["t"])
	}
}

// An unencodable float would make Encode fail and drop the line entirely, so
// the emitter must never be handed one. This pins the guard in place.
func TestLevelEventRefusesNonFiniteValues(t *testing.T) {
	em, buf, _ := newTestEmitter()
	em.Level(math.Inf(1), math.Inf(-1))
	got := decodeLines(t, buf.String())[0]
	if got["rms"] != 1.0 {
		t.Errorf("rms = %v, want 1 for +Inf", got["rms"])
	}
	if got["db"] != -60.0 {
		t.Errorf("db = %v, want -60 for -Inf", got["db"])
	}
}

func TestPartialAndCommit(t *testing.T) {
	em, buf, _ := newTestEmitter()
	em.Partial("so the thing is")
	em.Commit("So the thing is,")
	lines := decodeLines(t, buf.String())
	if lines[0]["type"] != "partial" || lines[0]["text"] != "so the thing is" {
		t.Errorf("partial line = %v", lines[0])
	}
	if lines[1]["type"] != "commit" || lines[1]["text"] != "So the thing is," {
		t.Errorf("commit line = %v", lines[1])
	}
}

// Transcripts contain apostrophes and angle brackets often enough that HTML
// escaping would be visible in the consumer's UI.
func TestTextIsNotHTMLEscaped(t *testing.T) {
	em, buf, _ := newTestEmitter()
	em.Partial(`5 < 6 && "quoted"`)
	if !strings.Contains(buf.String(), `5 < 6`) {
		t.Errorf("output was HTML-escaped: %s", buf.String())
	}
	if got := decodeLines(t, buf.String())[0]["text"]; got != `5 < 6 && "quoted"` {
		t.Errorf("round-trip = %q", got)
	}
}

func TestFinalEvent(t *testing.T) {
	em, buf, _ := newTestEmitter()
	em.Final(FinalEvent{
		Text:           "So the thing is, we shipped it.",
		Path:           "/tmp/memo.ogg",
		TranscriptPath: "/tmp/memo.txt",
		Backend:        "elevenlabs",
		Source:         SourceLive,
	})
	got := decodeLines(t, buf.String())[0]
	if got["type"] != "final" {
		t.Errorf("type = %v", got["type"])
	}
	if got["source"] != "live" {
		t.Errorf("source = %v", got["source"])
	}
	if got["transcript_path"] != "/tmp/memo.txt" {
		t.Errorf("transcript_path = %v", got["transcript_path"])
	}
}

func TestErrorEvent(t *testing.T) {
	em, buf, _ := newTestEmitter()
	em.Error(ScopeStream, false, errors.New("websocket dial failed"))
	got := decodeLines(t, buf.String())[0]
	if got["type"] != "error" {
		t.Errorf("type = %v", got["type"])
	}
	if got["scope"] != "stream" {
		t.Errorf("scope = %v", got["scope"])
	}
	if got["fatal"] != false {
		t.Errorf("fatal = %v, want false", got["fatal"])
	}
	if got["message"] != "websocket dial failed" {
		t.Errorf("message = %v", got["message"])
	}
}

// `fatal` must be present even when false, or a consumer cannot tell "not
// fatal" from "field missing".
func TestErrorEventAlwaysCarriesFatal(t *testing.T) {
	em, buf, _ := newTestEmitter()
	em.Error(ScopeRecord, false, errors.New("x"))
	if _, ok := decodeLines(t, buf.String())[0]["fatal"]; !ok {
		t.Error("fatal field omitted when false")
	}
}

func TestEndEvent(t *testing.T) {
	em, buf, _ := newTestEmitter()
	em.End(EndEvent{Reason: ReasonSignal, Path: "/tmp/memo.ogg", ExitCode: 0})
	got := decodeLines(t, buf.String())[0]
	if got["type"] != "end" {
		t.Errorf("type = %v", got["type"])
	}
	if got["reason"] != "signal" {
		t.Errorf("reason = %v", got["reason"])
	}
	if _, ok := got["exit_code"]; !ok {
		t.Error("exit_code omitted when zero")
	}
}

// Levels come off ffmpeg's stderr goroutine while partials come off the
// websocket goroutine. Interleaved half-lines would be unparseable.
func TestConcurrentEmitsProduceWholeLines(t *testing.T) {
	em, buf, _ := newTestEmitter()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); em.Level(0.5, -30) }()
		go func() { defer wg.Done(); em.Partial("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") }()
	}
	wg.Wait()
	if got := len(decodeLines(t, buf.String())); got != 100 {
		t.Errorf("parsed %d whole lines, want 100", got)
	}
}
