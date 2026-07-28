package tui

import "testing"

func TestDbToLevel(t *testing.T) {
	// -60dB should be ~0
	l := dbToLevel(-60.0)
	if l < 0 || l > 0.1 {
		t.Errorf("expected near 0, got %f", l)
	}
	// 0dB should be 1.0
	l = dbToLevel(0.0)
	if l != 1.0 {
		t.Errorf("expected 1.0, got %f", l)
	}
	// Below -60 should clamp to 0
	l = dbToLevel(-100.0)
	if l != 0 {
		t.Errorf("expected 0, got %f", l)
	}
	// Above 0 should clamp to 1
	l = dbToLevel(5.0)
	if l != 1.0 {
		t.Errorf("expected 1.0, got %f", l)
	}
}

func TestLevelToDB(t *testing.T) {
	db := levelToDB(1.0)
	if db != 0.0 {
		t.Errorf("expected 0 dB, got %f", db)
	}
	db = levelToDB(0.5)
	if db > -29 || db < -31 {
		t.Errorf("expected ~-30 dB, got %f", db)
	}
	db = levelToDB(0.0)
	if db > -99 {
		t.Errorf("expected very low dB, got %f", db)
	}
}

func TestFormatDB(t *testing.T) {
	s := formatDB(0.0)
	if s == "" {
		t.Error("expected non-empty string")
	}
	s = formatDB(1.0)
	if s == "" {
		t.Error("expected non-empty string")
	}
}

func TestVUMeterAttackDecay(t *testing.T) {
	var v VUMeter
	// Attack: level jumps to 1.0, smoothed moves halfway.
	v.Push(1.0)
	if l := v.Level(); l < 0.49 || l > 0.51 {
		t.Errorf("expected ~0.5 after attack push, got %f", l)
	}
	// Decay: level drops to 0, smoothed falls slowly (0.5 - 0.5*0.15 = 0.425).
	v.Push(0.0)
	if l := v.Level(); l < 0.42 || l > 0.43 {
		t.Errorf("expected ~0.425 after decay push, got %f", l)
	}
}

func TestVUMeterClamps(t *testing.T) {
	var v VUMeter
	for i := 0; i < 100; i++ {
		v.Push(5.0)
	}
	if l := v.Level(); l > 1.0 {
		t.Errorf("expected level clamped to 1.0, got %f", l)
	}
	for i := 0; i < 1000; i++ {
		v.Push(-5.0)
	}
	if l := v.Level(); l < 0 {
		t.Errorf("expected level clamped to 0, got %f", l)
	}
}

func TestVUCursorRune(t *testing.T) {
	if r := vuCursorRune(0.0); r != '▁' {
		t.Errorf("silence should floor at ▁, got %q", r)
	}
	if r := vuCursorRune(1.0); r != '█' {
		t.Errorf("full level should be █, got %q", r)
	}
	// Monotonic: higher level never yields a shorter block.
	prev := vuCursorRune(0.0)
	for l := 0.0; l <= 1.0; l += 0.05 {
		r := vuCursorRune(l)
		if r < prev {
			t.Errorf("cursor rune not monotonic at level %f: %q < %q", l, r, prev)
		}
		prev = r
	}
}

func TestRenderVUCursorColors(t *testing.T) {
	if got, want := renderVUCursor(0.0, false), waveGreen.Render("▁"); got != want {
		t.Errorf("low level: got %q want %q", got, want)
	}
	if got, want := renderVUCursor(0.7, false), waveYellow.Render(string(vuCursorRune(0.7))); got != want {
		t.Errorf("mid level: got %q want %q", got, want)
	}
	if got, want := renderVUCursor(1.0, false), waveRed.Render("█"); got != want {
		t.Errorf("peak level: got %q want %q", got, want)
	}
}

func TestRenderVUCursorPaused(t *testing.T) {
	got := renderVUCursor(0.9, true)
	want := vuCursorMutedStyle.Render("▁")
	if got != want {
		t.Errorf("paused cursor: got %q want %q", got, want)
	}
}
