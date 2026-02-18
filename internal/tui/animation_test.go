package tui

import "testing"

func TestAnimationRender(t *testing.T) {
	a := NewAnimation(30, 8)
	out := a.Render(0, 0.5, false)
	if out == "" {
		t.Error("expected non-empty render")
	}
}

func TestAnimationPaused(t *testing.T) {
	a := NewAnimation(30, 8)
	// Render two frames while paused - should be identical
	out1 := a.Render(0, 0.5, true)
	out2 := a.Render(5, 0.5, true)
	if out1 != out2 {
		t.Error("paused animation should produce same output regardless of tick")
	}
}

func TestAnimationRespondToLevel(t *testing.T) {
	a := NewAnimation(30, 8)
	quiet := a.Render(0, 0.0, false)
	loud := a.Render(0, 1.0, false)
	// They should differ since amplitude is modulated
	if quiet == loud {
		t.Error("expected different renders for different levels")
	}
}
