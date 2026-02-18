package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var vuDBText = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

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
