package tui

import (
	"fmt"
	"math"

	"github.com/charmbracelet/lipgloss"
	"github.com/joegoldin/audiomemo/internal/stream"
)

var vuDBText = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

var vuCursorMutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))

// Fractional block characters for the VU cursor, growing from the bottom of
// the cell. Index 0 = empty (unused by the cursor), 8 = full block.
var heightBlocks = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

var (
	waveGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	waveYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308"))
	waveRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
)

// dbToLevel maps a dBFS reading onto the 0..1 the meter draws. The mapping
// lives in internal/stream so the TUI and the --stream wire format cannot
// disagree about what "half" means.
func dbToLevel(db float64) float64 {
	return stream.NormalizeLevel(db)
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

// VUMeter smooths raw 0..1 levels with fast attack and slow decay, so peaks
// register immediately while the meter falls back gradually.
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
