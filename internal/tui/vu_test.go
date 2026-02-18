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
