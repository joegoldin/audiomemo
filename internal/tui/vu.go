package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	vuGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	vuYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308"))
	vuRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
	vuDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#404040"))
)

const (
	vuBlock = "████"
	vuEmpty = "┃   "
)

type VUMeter struct {
	height int
}

func NewVUMeter(height int) *VUMeter {
	return &VUMeter{height: height}
}

func dbToLevel(db float64) float64 {
	// Map -60..0 dB to 0..1
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
	filled := int(level * float64(v.height))

	var lines []string
	for i := v.height - 1; i >= 0; i-- {
		if i < filled {
			pct := float64(i) / float64(v.height)
			var style lipgloss.Style
			switch {
			case pct >= 0.8:
				style = vuRed
			case pct >= 0.5:
				style = vuYellow
			default:
				style = vuGreen
			}
			lines = append(lines, style.Render(vuBlock))
		} else {
			lines = append(lines, vuDim.Render(vuEmpty))
		}
	}
	return strings.Join(lines, "\n")
}
