package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Fractional block characters for smooth sub-character fill
var barBlocks = []rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉', '█'}

var (
	vuGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	vuYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308"))
	vuRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
	vuDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
	vuDBText = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
)

type VUMeter struct {
	width      int
	smoothed   float64
	smoothedDB float64
	hasValue   bool
}

func NewVUMeter(width int) *VUMeter {
	return &VUMeter{width: width, smoothedDB: -60}
}

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

func (v *VUMeter) Render(db float64) string {
	level := dbToLevel(db)

	// Smooth interpolation (moderate attack, slow decay)
	diff := level - v.smoothed
	if diff > 0 {
		v.smoothed += diff * 0.45
	} else {
		v.smoothed += diff * 0.12
	}
	v.smoothed = math.Max(0, math.Min(1, v.smoothed))

	// Smooth dB for display
	clampedDB := math.Max(-60, math.Min(0, db))
	dbDiff := clampedDB - v.smoothedDB
	if dbDiff > 0 {
		v.smoothedDB += dbDiff * 0.45
	} else {
		v.smoothedDB += dbDiff * 0.12
	}

	fillFloat := v.smoothed * float64(v.width)
	fullBlocks := int(fillFloat)
	frac := fillFloat - float64(fullBlocks)
	fracIdx := int(frac * float64(len(barBlocks)-1))

	var b strings.Builder
	for i := 0; i < v.width; i++ {
		pct := float64(i) / float64(v.width)
		var style lipgloss.Style
		switch {
		case pct >= 0.85:
			style = vuRed
		case pct >= 0.6:
			style = vuYellow
		default:
			style = vuGreen
		}

		if i < fullBlocks {
			b.WriteString(style.Render("█"))
		} else if i == fullBlocks && fracIdx > 0 {
			b.WriteString(style.Render(string(barBlocks[fracIdx])))
		} else {
			b.WriteString(vuDim.Render("░"))
		}
	}

	// Show smoothed dB value
	dbStr := "  -∞"
	if v.smoothedDB > -59 {
		dbStr = fmt.Sprintf(" %4.1fdB", v.smoothedDB)
	}

	return b.String() + vuDBText.Render(dbStr)
}
