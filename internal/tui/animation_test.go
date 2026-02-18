package tui

import (
	"strings"
	"testing"
)

func TestAnimationRender(t *testing.T) {
	a := NewAnimation(30, 9)
	out := a.Render(0, 0.5, false)
	if out == "" {
		t.Error("expected non-empty render")
	}
	// Should have 9 lines (one per row)
	lines := strings.Split(out, "\n")
	if len(lines) != 9 {
		t.Errorf("expected 9 lines, got %d", len(lines))
	}
}

func TestAnimationPaused(t *testing.T) {
	a := NewAnimation(30, 9)
	// Render two frames while paused — should be identical
	out1 := a.Render(0, 0.5, true)
	out2 := a.Render(5, 0.5, true)
	if out1 != out2 {
		t.Error("paused animation should produce same output regardless of tick")
	}
}

func TestAnimationRespondToLevel(t *testing.T) {
	a := NewAnimation(30, 9)
	quiet := a.Render(0, 0.0, false)
	loud := a.Render(0, 1.0, false)
	if quiet == loud {
		t.Error("expected different renders for different levels")
	}
}

func TestAnimationFlatWhenSilent(t *testing.T) {
	a := NewAnimation(30, 9)
	out := a.Render(0, 0.0, false)
	// When level is 0, should have no bar blocks
	for _, r := range out {
		if r == '█' {
			t.Error("expected no bar blocks when level is 0")
			return
		}
	}
}

func TestAnimationBarsWhenLoud(t *testing.T) {
	a := NewAnimation(30, 9)
	out := a.Render(0, 1.0, false)
	found := false
	for _, r := range out {
		if r == '█' {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected bar blocks when level is 1.0")
	}
}

func TestAnimationFractionalBlocks(t *testing.T) {
	a := NewAnimation(30, 9)
	// Push several moderate levels to get fractional edges
	for i := 0; i < 35; i++ {
		a.Push(0.3)
	}
	out := a.Render(0, 0.3, false)
	hasFractional := false
	for _, r := range out {
		if r >= '▁' && r <= '▇' {
			hasFractional = true
			break
		}
	}
	if !hasFractional {
		t.Error("expected fractional block characters for moderate levels")
	}
}

func TestAnimationTickMarks(t *testing.T) {
	a := NewAnimation(30, 9)
	// Push enough to get tick marks scrolling
	for i := 0; i < 40; i++ {
		a.Push(0.0)
	}
	out := a.Render(0, 0.0, false)
	// Should contain tick characters
	hasTick := strings.Contains(out, "┊") || strings.Contains(out, "·")
	if !hasTick {
		t.Error("expected tick marks in silent render")
	}
}

func TestAnimationSmoothedLevel(t *testing.T) {
	a := NewAnimation(30, 9)
	if a.SmoothedLevel() != 0 {
		t.Error("expected 0 smoothed level initially")
	}
	a.Push(1.0)
	if a.SmoothedLevel() <= 0 {
		t.Error("expected positive smoothed level after loud push")
	}
}
