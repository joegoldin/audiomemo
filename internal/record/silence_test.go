package record

import (
	"math"
	"testing"
	"time"
)

var silenceBase = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

func TestNewSilenceWatcherDisabled(t *testing.T) {
	for _, limit := range []time.Duration{0, -time.Second} {
		w := NewSilenceWatcher(-40, limit)
		if w != nil {
			t.Fatalf("NewSilenceWatcher(-40, %v) = %v, want nil", limit, w)
		}
		if w.Push(-90, silenceBase) {
			t.Fatal("nil watcher fired")
		}
	}
}

func TestSilenceWatcherWaitsForSpeech(t *testing.T) {
	w := NewSilenceWatcher(-40, time.Second)
	for i := 0; i < 100; i++ {
		at := silenceBase.Add(time.Duration(i) * 100 * time.Millisecond)
		if w.Push(-70, at) {
			t.Fatalf("fired on silence at %v with no speech yet", at.Sub(silenceBase))
		}
	}
}

func TestSilenceWatcherFiresOnTrailingSilence(t *testing.T) {
	w := NewSilenceWatcher(-40, 2*time.Second)

	if w.Push(-20, silenceBase) {
		t.Fatal("fired on a loud reading")
	}
	// Silence is measured from the last loud reading, not from the first
	// quiet one: the room went quiet when the speaking stopped.
	if w.Push(-70, silenceBase.Add(500*time.Millisecond)) {
		t.Fatal("fired 0.5s into a 2s limit")
	}
	if w.Push(-70, silenceBase.Add(1900*time.Millisecond)) {
		t.Fatal("fired 1.9s into a 2s limit")
	}
	if !w.Push(-70, silenceBase.Add(2*time.Second)) {
		t.Fatal("did not fire after 2s of silence")
	}
}

func TestSilenceWatcherLoudReadingResetsClock(t *testing.T) {
	w := NewSilenceWatcher(-40, time.Second)

	w.Push(-20, silenceBase)
	w.Push(-70, silenceBase.Add(100*time.Millisecond))
	// Speech resumes just before the limit would have been reached.
	w.Push(-25, silenceBase.Add(900*time.Millisecond))
	if w.Push(-70, silenceBase.Add(1500*time.Millisecond)) {
		t.Fatal("fired using silence from before the loud reading")
	}
	if !w.Push(-70, silenceBase.Add(2*time.Second)) {
		t.Fatal("did not fire after a fresh second of silence")
	}
}

func TestSilenceWatcherFiresOnce(t *testing.T) {
	w := NewSilenceWatcher(-40, time.Second)

	w.Push(-20, silenceBase)
	if !w.Push(-70, silenceBase.Add(2*time.Second)) {
		t.Fatal("did not fire after the limit")
	}
	if w.Push(-70, silenceBase.Add(3*time.Second)) {
		t.Fatal("fired a second time")
	}
}

func TestSilenceWatcherThresholdBoundary(t *testing.T) {
	tests := []struct {
		name string
		db   float64
		want bool
	}{
		{name: "negative infinity is silence", db: math.Inf(-1), want: true},
		{name: "reading at the threshold is silence", db: -40, want: true},
		{name: "reading above the threshold is speech", db: -39.9, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewSilenceWatcher(-40, time.Second)
			w.Push(-20, silenceBase)
			got := w.Push(tt.db, silenceBase.Add(2*time.Second))
			if got != tt.want {
				t.Fatalf("Push(%v) = %v, want %v", tt.db, got, tt.want)
			}
		})
	}
}
