package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Block height characters for smooth vertical resolution per column
var heightBlocks = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

var (
	waveDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#1e1b4b"))
	waveLow    = lipgloss.NewStyle().Foreground(lipgloss.Color("#4c1d95"))
	waveMid    = lipgloss.NewStyle().Foreground(lipgloss.Color("#7c3aed"))
	waveHigh   = lipgloss.NewStyle().Foreground(lipgloss.Color("#a78bfa"))
	wavePeak   = lipgloss.NewStyle().Foreground(lipgloss.Color("#c4b5fd"))
	waveSilent = lipgloss.NewStyle().Foreground(lipgloss.Color("#2e1065"))
)

const flatLine = '─'

// Animation displays a scrolling waveform of recent audio levels.
// Each column represents one level sample, rendered as a mirrored
// bar growing up and down from the center line.
type Animation struct {
	width  int
	height int
	// Circular buffer of recent level values (0..1)
	history []float64
	cursor  int
	// Smoothed level for current frame
	smoothed float64
	// Track last pushed level to avoid filling with stale repeats
	lastLevel float64
	staleCount int
}

func NewAnimation(width, height int) *Animation {
	return &Animation{
		width:   width,
		height:  height,
		history: make([]float64, width),
	}
}

// Push adds a new level sample to the scrolling history.
func (a *Animation) Push(level float64) {
	// Smooth the input (moderate attack, gradual decay)
	diff := level - a.smoothed
	if diff > 0 {
		a.smoothed += diff * 0.5
	} else {
		a.smoothed += diff * 0.15
	}
	a.smoothed = math.Max(0, math.Min(1, a.smoothed))

	a.history[a.cursor] = a.smoothed
	a.cursor = (a.cursor + 1) % a.width
}

func (a *Animation) Render(tick int, level float64, paused bool) string {
	if !paused {
		// Only push a new column when the level actually changed,
		// so repeated ticks with stale data don't flood the buffer.
		if level != a.lastLevel {
			a.lastLevel = level
			a.staleCount = 0
			a.Push(level)
		} else {
			a.staleCount++
			// Still push occasionally so the waveform scrolls during
			// sustained tones, but decay toward silence smoothly.
			if a.staleCount%3 == 0 {
				a.Push(level)
			}
		}
	}

	centerY := a.height / 2

	// Build grid
	grid := make([][]rune, a.height)
	for y := range grid {
		grid[y] = make([]rune, a.width)
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}

	// Draw each column from the history buffer (oldest to newest)
	for col := 0; col < a.width; col++ {
		idx := (a.cursor + col) % a.width
		h := a.history[idx]

		if h < 0.02 {
			// Flat center line when silent
			grid[centerY][col] = flatLine
		} else {
			// Mirrored bars from center
			extent := int(math.Round(h * float64(centerY)))
			if extent < 1 {
				extent = 1
			}
			for dy := 0; dy <= extent && centerY-dy >= 0 && centerY+dy < a.height; dy++ {
				if dy == 0 {
					grid[centerY][col] = '█'
				} else {
					grid[centerY-dy][col] = '█'
					grid[centerY+dy][col] = '█'
				}
			}
		}
	}

	// Render with color based on row distance from center
	var lines []string
	for y, row := range grid {
		dist := math.Abs(float64(y-centerY)) / math.Max(1, float64(centerY))
		var style lipgloss.Style
		switch {
		case dist > 0.8:
			style = wavePeak
		case dist > 0.55:
			style = waveHigh
		case dist > 0.3:
			style = waveMid
		case dist > 0.05:
			style = waveLow
		default:
			style = waveDim
		}

		// Use dim style for flat-line-only rows
		hasBar := false
		for _, r := range row {
			if r == '█' {
				hasBar = true
				break
			}
		}
		if !hasBar {
			style = waveSilent
		}

		lines = append(lines, style.Render(string(row)))
	}
	return strings.Join(lines, "\n")
}
