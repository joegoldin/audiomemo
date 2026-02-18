package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var animDot = lipgloss.NewStyle().Foreground(lipgloss.Color("#7c3aed"))

type Animation struct {
	width     int
	height    int
	pauseTick int
}

func NewAnimation(width, height int) *Animation {
	return &Animation{width: width, height: height}
}

func (a *Animation) Render(tick int, level float64, paused bool) string {
	activeTick := tick
	if paused {
		activeTick = a.pauseTick
	} else {
		a.pauseTick = tick
	}

	// Base amplitude + level modulation
	baseAmp := 0.3 + level*0.7
	centerY := float64(a.height) / 2.0
	phase := float64(activeTick) * 0.15

	grid := make([][]rune, a.height)
	for y := range grid {
		grid[y] = make([]rune, a.width)
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}

	for x := 0; x < a.width; x++ {
		xf := float64(x)
		// Combine two sine waves for organic feel
		y1 := math.Sin(xf*0.25+phase) * baseAmp * centerY * 0.6
		y2 := math.Sin(xf*0.4+phase*1.3+1.0) * baseAmp * centerY * 0.3

		py := centerY + y1 + y2
		iy := int(math.Round(py))
		if iy >= 0 && iy < a.height {
			grid[iy][x] = '·'
		}
	}

	var lines []string
	for _, row := range grid {
		lines = append(lines, animDot.Render(string(row)))
	}
	return strings.Join(lines, "\n")
}
