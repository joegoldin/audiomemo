package stream

import (
	"math"
	"testing"
	"time"
)

func TestNormalizeLevel(t *testing.T) {
	cases := []struct {
		db   float64
		want float64
	}{
		{0, 1},     // full scale
		{-30, 0.5}, // halfway up the -60 dBFS floor
		{-60, 0},   // the floor itself
		{-90, 0},   // below the floor clamps rather than going negative
		{6, 1},     // above full scale clamps
		{math.Inf(-1), 0},
		{math.Inf(1), 1},
		{math.NaN(), 0},
	}
	for _, c := range cases {
		if got := NormalizeLevel(c.db); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("NormalizeLevel(%v) = %v, want %v", c.db, got, c.want)
		}
	}
}

func TestClampDB(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{-21.5, -21.5},
		{-120, FloorDB},
		{4, 0},
		{math.Inf(-1), FloorDB},
		{math.Inf(1), 0},
		{math.NaN(), FloorDB},
	}
	for _, c := range cases {
		if got := ClampDB(c.in); got != c.want {
			t.Errorf("ClampDB(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLevelThrottleEmitsFirstSampleImmediately(t *testing.T) {
	th := newLevelThrottle(50*time.Millisecond, clockAt(0))
	got, ok := th.Push(-20)
	if !ok {
		t.Fatal("first sample was withheld; the meter would start blank")
	}
	if got != -20 {
		t.Errorf("got %v, want -20", got)
	}
}

func TestLevelThrottleKeepsThePeakWithinAWindow(t *testing.T) {
	clk := &fakeClock{at: time.Unix(0, 0)}
	th := newLevelThrottle(50*time.Millisecond, clk.now)
	th.Push(-40) // emitted immediately

	clk.at = clk.at.Add(10 * time.Millisecond)
	if _, ok := th.Push(-35); ok {
		t.Error("sample inside the window was emitted")
	}
	clk.at = clk.at.Add(10 * time.Millisecond)
	if _, ok := th.Push(-12); ok { // the peak
		t.Error("sample inside the window was emitted")
	}
	clk.at = clk.at.Add(10 * time.Millisecond)
	if _, ok := th.Push(-38); ok {
		t.Error("sample inside the window was emitted")
	}

	clk.at = clk.at.Add(30 * time.Millisecond) // window closed
	got, ok := th.Push(-45)
	if !ok {
		t.Fatal("window closed but nothing was emitted")
	}
	// A meter that reported -45 here would miss the transient entirely.
	if got != -12 {
		t.Errorf("got %v, want the window peak -12", got)
	}
}

func TestLevelThrottleStartsAFreshWindowAfterEmitting(t *testing.T) {
	clk := &fakeClock{at: time.Unix(0, 0)}
	th := newLevelThrottle(50*time.Millisecond, clk.now)
	th.Push(-10)
	clk.at = clk.at.Add(60 * time.Millisecond)
	th.Push(-50)
	clk.at = clk.at.Add(60 * time.Millisecond)
	got, _ := th.Push(-55)
	if got != -55 {
		t.Errorf("got %v, want -55; the previous window's peak leaked", got)
	}
}

func clockAt(sec int64) func() time.Time {
	return func() time.Time { return time.Unix(sec, 0) }
}
