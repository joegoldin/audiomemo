package tui

import (
	"math"
	"math/rand"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	animLow  = lipgloss.NewStyle().Foreground(lipgloss.Color("#4c1d95"))
	animMid  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7c3aed"))
	animHigh = lipgloss.NewStyle().Foreground(lipgloss.Color("#a78bfa"))
	animPeak = lipgloss.NewStyle().Foreground(lipgloss.Color("#c4b5fd"))
	animDim  = lipgloss.NewStyle().Foreground(lipgloss.Color("#2e1065"))
)

const barBlock = '█'
const flatLine = '─'

type Animation struct {
	width     int
	height    int
	pauseTick int
	// Per-column "energy" values for spectroscope bars
	bars []float64
	// Smoothed targets for organic movement
	targets []float64
	rng     *rand.Rand
}

func NewAnimation(width, height int) *Animation {
	a := &Animation{
		width:   width,
		height:  height,
		bars:    make([]float64, width),
		targets: make([]float64, width),
		rng:     rand.New(rand.NewSource(42)),
	}
	return a
}

func (a *Animation) Render(tick int, level float64, paused bool) string {
	activeTick := tick
	if paused {
		activeTick = a.pauseTick
	} else {
		a.pauseTick = tick
	}

	centerY := a.height / 2

	// Update bar targets based on audio level
	if !paused {
		a.updateBars(activeTick, level)
	}

	grid := make([][]rune, a.height)
	for y := range grid {
		grid[y] = make([]rune, a.width)
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}

	// Draw flat center line when quiet, spectroscope bars when loud
	for x := 0; x < a.width; x++ {
		barHeight := a.bars[x]

		if barHeight < 0.05 {
			// Flat line at center
			grid[centerY][x] = flatLine
		} else {
			// Draw bars extending up and down from center (mirrored)
			extent := int(math.Round(barHeight * float64(centerY)))
			if extent < 1 {
				extent = 1
			}
			for dy := 0; dy <= extent && centerY-dy >= 0 && centerY+dy < a.height; dy++ {
				if dy == 0 {
					grid[centerY][x] = barBlock
				} else {
					grid[centerY-dy][x] = barBlock
					grid[centerY+dy][x] = barBlock
				}
			}
		}
	}

	// Render with color based on row distance from center
	var lines []string
	for y, row := range grid {
		dist := math.Abs(float64(y-centerY)) / float64(centerY)
		var style lipgloss.Style
		switch {
		case dist > 0.75:
			style = animPeak
		case dist > 0.5:
			style = animHigh
		case dist > 0.2:
			style = animMid
		default:
			style = animLow
		}

		// Dim the flat-line character
		line := string(row)
		hasContent := false
		for _, r := range row {
			if r != ' ' && r != flatLine {
				hasContent = true
				break
			}
		}
		if !hasContent {
			style = animDim
		}

		lines = append(lines, style.Render(line))
	}
	return strings.Join(lines, "\n")
}

func (a *Animation) updateBars(tick int, level float64) {
	// Generate new random targets periodically
	phase := float64(tick) * 0.2

	for x := 0; x < a.width; x++ {
		xf := float64(x)

		// Multiple frequency components for spectroscope look
		f1 := math.Sin(xf*0.3+phase) * 0.5
		f2 := math.Sin(xf*0.7+phase*1.6+2.0) * 0.3
		f3 := math.Sin(xf*1.1+phase*0.7+4.0) * 0.2

		// Random jitter for organic feel
		jitter := (a.rng.Float64() - 0.5) * 0.3

		// Target bar height: 0 when silent, full spectroscope when loud
		raw := (f1 + f2 + f3 + jitter + 1.0) / 2.0 // normalize to 0..1
		target := raw * level

		a.targets[x] = target

		// Smooth interpolation toward target (fast attack, slow decay)
		diff := a.targets[x] - a.bars[x]
		if diff > 0 {
			// Attack: fast rise
			a.bars[x] += diff * 0.6
		} else {
			// Decay: slow fall
			a.bars[x] += diff * 0.15
		}

		// Clamp
		if a.bars[x] < 0 {
			a.bars[x] = 0
		}
		if a.bars[x] > 1 {
			a.bars[x] = 1
		}
	}
}
