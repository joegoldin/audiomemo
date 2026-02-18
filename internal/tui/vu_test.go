package tui

import "testing"

func TestVUMeterRender(t *testing.T) {
	vu := NewVUMeter(10)
	// Silent: should be mostly empty
	out := vu.Render(-60.0)
	if out == "" {
		t.Error("expected non-empty render")
	}

	// Loud: should have more filled blocks
	out2 := vu.Render(-6.0)
	if out2 == "" {
		t.Error("expected non-empty render")
	}
}

func TestVUMeterClamp(t *testing.T) {
	vu := NewVUMeter(10)
	// Very quiet
	out1 := vu.Render(-100.0)
	// Clipping
	out2 := vu.Render(0.0)
	// Both should render without panic
	if out1 == "" || out2 == "" {
		t.Error("expected renders for extreme values")
	}
}

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
}
