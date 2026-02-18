package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Fractional block characters for smooth vertical sub-cell resolution.
// Index 0 = empty, 8 = full block. These grow from the bottom of the cell.
var heightBlocks = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

var (
	waveGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	waveYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308"))
	waveRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
	waveTick   = lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
	waveTickHi = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
)

// Irregular tick pattern — asymmetric spacing makes scrolling motion visible.
// The pattern repeats every 13 columns: major at 0, minor at 4 and 9.
const tickCycle = 13

func isMajorTick(pos int) bool {
	m := pos % tickCycle
	return m == 0
}

func isMinorTick(pos int) bool {
	m := pos % tickCycle
	return m == 4 || m == 9
}

// Animation displays a scrolling waveform of recent audio levels.
// Each column is a vertical bar growing from bottom to top, colored
// green (low) to yellow (mid) to red (peak). The rightmost column is
// the live VU; older columns scroll left as history. Tick marks scroll
// through to show movement even during silence.
type Animation struct {
	width  int
	height int
	// Circular buffer of recent level values (0..1)
	history []float64
	cursor  int
	// Smoothed level for current frame
	smoothed float64
	// Track last pushed level to avoid filling with stale repeats
	lastLevel  float64
	staleCount int
	// Total pushes for scrolling tick marks
	totalPushes int
}

func NewAnimation(width, height int) *Animation {
	return &Animation{
		width:   width,
		height:  height,
		history: make([]float64, width),
	}
}

// SmoothedLevel returns the current smoothed level (0..1) for dB display.
func (a *Animation) SmoothedLevel() float64 {
	return a.smoothed
}

// Push adds a new level sample to the scrolling history.
func (a *Animation) Push(level float64) {
	// Heavy smoothing for the dB readout (moderate attack, slow decay).
	diff := level - a.smoothed
	if diff > 0 {
		a.smoothed += diff * 0.5
	} else {
		a.smoothed += diff * 0.15
	}
	a.smoothed = math.Max(0, math.Min(1, a.smoothed))

	// Light smoothing for the waveform bars — lets natural variation through
	// so adjacent columns differ, looking more like an actual waveform.
	prevBar := a.history[(a.cursor-1+a.width)%a.width]
	bar := prevBar + (level-prevBar)*0.7
	bar = math.Max(0, math.Min(1, bar))
	a.history[a.cursor] = bar
	a.cursor = (a.cursor + 1) % a.width
	a.totalPushes++
}

func (a *Animation) Render(tick int, level float64, paused bool) string {
	if !paused {
		if level != a.lastLevel {
			a.lastLevel = level
			a.staleCount = 0
			a.Push(level)
		} else {
			a.staleCount++
			if a.staleCount%3 == 0 {
				a.Push(level)
			}
		}
	}

	// Build grid. Row 0 = top (peak/red), row height-1 = bottom (green).
	grid := make([][]rune, a.height)
	for y := range grid {
		grid[y] = make([]rune, a.width)
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}

	// Draw each column as a bottom-to-top bar.
	for col := 0; col < a.width; col++ {
		idx := (a.cursor + col) % a.width
		h := a.history[idx]

		if h < 0.005 {
			continue // empty column, ticks drawn later
		}

		// Continuous fill height in cell units (bottom-up)
		fillFloat := h * float64(a.height)
		fullCells := int(fillFloat)
		frac := fillFloat - float64(fullCells)
		fracIdx := int(frac * 8)

		// Fill from bottom up
		for i := 0; i < fullCells && i < a.height; i++ {
			y := a.height - 1 - i
			grid[y][col] = '█'
		}

		// Fractional top edge
		if fracIdx > 0 && fullCells < a.height {
			y := a.height - 1 - fullCells
			grid[y][col] = heightBlocks[fracIdx]
		}
	}

	// Render each row with color based on vertical position.
	// Bottom = green, mid = yellow, top = red.
	var lines []string
	for y, row := range grid {
		// heightFrac: 0.0 at bottom, 1.0 at top
		heightFrac := float64(a.height-1-y) / math.Max(1, float64(a.height-1))
		var barStyle lipgloss.Style
		switch {
		case heightFrac >= 0.85:
			barStyle = waveRed
		case heightFrac >= 0.6:
			barStyle = waveYellow
		default:
			barStyle = waveGreen
		}

		var b strings.Builder
		for col, r := range row {
			if r == '█' || (r >= '▁' && r <= '▇') {
				b.WriteString(barStyle.Render(string(r)))
			} else {
				// Empty cell: show scrolling tick marks (irregular spacing).
				age := a.width - 1 - col
				absPos := a.totalPushes - age
				if isMajorTick(absPos) {
					b.WriteString(waveTickHi.Render("┊"))
				} else if isMinorTick(absPos) {
					b.WriteString(waveTick.Render("·"))
				} else {
					b.WriteRune(' ')
				}
			}
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}
