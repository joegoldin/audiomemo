package tui

import (
	"fmt"
	"math"

	"github.com/charmbracelet/lipgloss"
)

var vuDBText = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

var vuCursorMutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))

func dbToLevel(db float64) float64 {
	const minDB = -60.0
	if db <= minDB {
		return 0
	}
	if db >= 0 {
		return 1.0
	}
	return (db - minDB) / (0 - minDB)
}

func levelToDB(level float64) float64 {
	if level <= 0 {
		return -100
	}
	return -60.0 * (1.0 - level)
}

// formatDB returns a smoothed dB readout string for display.
func formatDB(smoothedLevel float64) string {
	if smoothedLevel < 0.01 {
		return "  -∞ dB"
	}
	db := levelToDB(smoothedLevel)
	return fmt.Sprintf("%4.1f dB", db)
}

// VUMeter smooths raw 0..1 levels with fast attack and slow decay — the same
// feel the old waveform's dB readout had.
type VUMeter struct {
	smoothed float64
}

// Push feeds one raw level sample (0..1) into the meter.
func (v *VUMeter) Push(level float64) {
	diff := level - v.smoothed
	if diff > 0 {
		v.smoothed += diff * 0.5
	} else {
		v.smoothed += diff * 0.15
	}
	v.smoothed = math.Max(0, math.Min(1, v.smoothed))
}

// Level returns the current smoothed level (0..1).
func (v *VUMeter) Level() float64 {
	return v.smoothed
}

// vuCursorRune maps a smoothed 0..1 level to a block rune. Silence floors at
// ▁ so the cursor never disappears.
func vuCursorRune(level float64) rune {
	idx := 1 + int(math.Round(level*7))
	if idx < 1 {
		idx = 1
	}
	if idx > 8 {
		idx = 8
	}
	return heightBlocks[idx]
}

// renderVUCursor renders the single-cell VU cursor: a block rune whose height
// and color track the mic level. When paused (muted, READY, or saved) it is a
// static dim-gray ▁.
func renderVUCursor(level float64, paused bool) string {
	if paused {
		return vuCursorMutedStyle.Render("▁")
	}
	r := string(vuCursorRune(level))
	switch {
	case level >= 0.85:
		return waveRed.Render(r)
	case level >= 0.6:
		return waveYellow.Render(r)
	default:
		return waveGreen.Render(r)
	}
}
