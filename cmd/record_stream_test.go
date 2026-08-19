package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/stream"
)

func decodeStreamLines(t *testing.T, s string) []map[string]any {
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

func TestPumpLevelsEmitsBothScalesAndStopsOnClose(t *testing.T) {
	buf := &bytes.Buffer{}
	em := stream.NewEmitter(buf)
	levels := make(chan float64, 4)
	levels <- -30.0
	close(levels)

	done := make(chan struct{})
	go func() { pumpLevels(em, levels, stream.NewLevelThrottle(0)); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pumpLevels did not return when its channel closed")
	}

	lines := decodeStreamLines(t, buf.String())
	if len(lines) != 1 {
		t.Fatalf("want 1 level line, got %d", len(lines))
	}
	if lines[0]["type"] != "level" {
		t.Errorf("type = %v", lines[0]["type"])
	}
	if lines[0]["db"] != -30.0 {
		t.Errorf("db = %v, want -30", lines[0]["db"])
	}
	if lines[0]["rms"] != 0.5 {
		t.Errorf("rms = %v, want 0.5 halfway up the -60 floor", lines[0]["rms"])
	}
}

// ffmpeg reports digital silence as -inf. Without the clamp this line would
// fail to encode and vanish, taking the meter with it.
func TestPumpLevelsSurvivesDigitalSilence(t *testing.T) {
	buf := &bytes.Buffer{}
	levels := make(chan float64, 1)
	levels <- math.Inf(-1)
	close(levels)
	pumpLevels(stream.NewEmitter(buf), levels, stream.NewLevelThrottle(0))

	lines := decodeStreamLines(t, buf.String())
	if len(lines) != 1 {
		t.Fatalf("silence produced %d lines, want 1", len(lines))
	}
	if lines[0]["rms"] != 0.0 || lines[0]["db"] != -60.0 {
		t.Errorf("silence encoded as rms=%v db=%v, want 0 and -60", lines[0]["rms"], lines[0]["db"])
	}
}

func TestPumpTextSeparatesPartialFromCommit(t *testing.T) {
	buf := &bytes.Buffer{}
	partial := make(chan string, 2)
	committed := make(chan string, 2)
	partial <- "so the thing"
	committed <- "So the thing is,"
	close(partial)
	close(committed)

	pumpText(stream.NewEmitter(buf), partial, committed)

	types := map[string]string{}
	for _, l := range decodeStreamLines(t, buf.String()) {
		types[l["type"].(string)] = l["text"].(string)
	}
	if types["partial"] != "so the thing" {
		t.Errorf("partial = %q", types["partial"])
	}
	if types["commit"] != "So the thing is," {
		t.Errorf("commit = %q", types["commit"])
	}
}

// The Streamer's Err channel carries session failures that do not stop the
// recording. They must reach the consumer as events, not as stderr noise it
// never sees.
func TestPumpErrorsMarksSessionFailuresNonFatal(t *testing.T) {
	buf := &bytes.Buffer{}
	errs := make(chan error, 1)
	errs <- errors.New("elevenlabs error (rate_limited): slow down")
	close(errs)

	pumpErrors(stream.NewEmitter(buf), errs)

	line := decodeStreamLines(t, buf.String())[0]
	if line["type"] != "error" {
		t.Errorf("type = %v", line["type"])
	}
	if line["scope"] != "stream" {
		t.Errorf("scope = %v, want stream", line["scope"])
	}
	if line["fatal"] != false {
		t.Errorf("fatal = %v; the recording keeps going after a stream error", line["fatal"])
	}
	if !strings.Contains(line["message"].(string), "rate_limited") {
		t.Errorf("message = %v", line["message"])
	}
}

func TestEndReasonForSignal(t *testing.T) {
	if got := endReason(true, nil); got != stream.ReasonSignal {
		t.Errorf("endReason(signalled) = %q, want signal", got)
	}
	if got := endReason(false, nil); got != stream.ReasonStopped {
		t.Errorf("endReason(clean) = %q, want stopped", got)
	}
	if got := endReason(false, errors.New("boom")); got != stream.ReasonError {
		t.Errorf("endReason(failed) = %q, want error", got)
	}
	// A signal is a deliberate stop, so it outranks whatever non-zero status
	// ffmpeg produced while tearing down.
	if got := endReason(true, errors.New("exit status 255")); got != stream.ReasonSignal {
		t.Errorf("endReason(signalled, ffmpeg error) = %q, want signal", got)
	}
}

func TestEndExitCode(t *testing.T) {
	if got := endExitCode(true, errors.New("exit status 255")); got != 0 {
		t.Errorf("a deliberate stop is not a failure, got exit_code %d", got)
	}
	if got := endExitCode(false, errors.New("boom")); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
	if got := endExitCode(false, nil); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}
