package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joegilkes/audiotools/internal/record"
)

type State int

const (
	StateRecording State = iota
	StatePaused
	StateSaved
)

type Model struct {
	state      State
	recorder   *record.Recorder
	opts       record.RecordOpts
	startTime  time.Time
	elapsed    time.Duration
	pauseStart time.Time
	pauseTotal time.Duration
	level      float64
	tick       int
	vu         *VUMeter
	anim       *Animation
	picker     *DevicePicker
	showPicker bool
	transcribe bool // set when user presses Q to quit-and-transcribe
	err        error
	width      int
	height     int
}

// ShouldTranscribe returns true if the user pressed Q to quit-and-transcribe.
func (m *Model) ShouldTranscribe() bool {
	return m.transcribe
}

type tickMsg time.Time
type levelMsg float64
type doneMsg error

func NewModel(rec *record.Recorder, opts record.RecordOpts) *Model {
	return &Model{
		state:     StateRecording,
		recorder:  rec,
		opts:      opts,
		startTime: time.Now(),
		vu:        NewVUMeter(50),
		anim:      NewAnimation(50, 7),
		picker:    NewDevicePicker(),
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), listenLevel(m.recorder), listenDone(m.recorder))
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*33, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func listenLevel(rec *record.Recorder) tea.Cmd {
	return func() tea.Msg {
		level, ok := <-rec.Level
		if !ok {
			return nil
		}
		return levelMsg(level)
	}
}

func listenDone(rec *record.Recorder) tea.Cmd {
	return func() tea.Msg {
		err := <-rec.Done
		return doneMsg(err)
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.showPicker {
			return m.updatePicker(msg)
		}
		return m.handleKey(msg)

	case tickMsg:
		if m.state == StateRecording {
			m.elapsed = time.Since(m.startTime) - m.pauseTotal
			m.tick++
		}
		return m, tickCmd()

	case levelMsg:
		m.level = float64(msg)
		return m, listenLevel(m.recorder)

	case doneMsg:
		m.state = StateSaved
		if msg != nil {
			m.err = error(msg)
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))):
		m.recorder.Stop()
		m.state = StateSaved
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("q"))):
		m.recorder.Stop()
		m.state = StateSaved
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("Q"))):
		m.recorder.Stop()
		m.state = StateSaved
		m.transcribe = true
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("p", " "))):
		if m.state == StateRecording {
			m.state = StatePaused
			m.pauseStart = time.Now()
			m.recorder.Pause()
		} else if m.state == StatePaused {
			m.state = StateRecording
			m.pauseTotal += time.Since(m.pauseStart)
			m.recorder.Pause()
		}
		return m, nil

	case key.Matches(msg, key.NewBinding(key.WithKeys("d"))):
		m.showPicker = true
		return m, nil
	}
	return m, nil
}

func (m *Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc", "q"))):
		m.showPicker = false
	}
	return m, nil
}

var (
	recStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Bold(true)
	pauseStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308")).Bold(true)
	savedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	infoStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a1a1aa"))
)

func (m *Model) View() string {
	if m.showPicker {
		return m.picker.View()
	}

	// Status line
	var status string
	switch m.state {
	case StateRecording:
		status = recStyle.Render("● REC")
	case StatePaused:
		status = pauseStyle.Render("⏸ PAUSED")
	case StateSaved:
		status = savedStyle.Render("✓ SAVED")
	}

	dur := formatDuration(m.elapsed)
	info := fmt.Sprintf("%dkHz %s", m.opts.SampleRate/1000, channelStr(m.opts.Channels))
	header := fmt.Sprintf("  %s  %s       %s", status, dur, dimStyle.Render(info))

	// Animation
	paused := m.state != StateRecording
	animLevel := dbToLevel(m.level)
	animView := m.anim.Render(m.tick, animLevel, paused)

	// VU
	vuView := m.vu.Render(m.level)

	// Stack animation and VU vertically
	center := lipgloss.JoinVertical(lipgloss.Left, animView, "  "+vuView)

	// Info
	micLine := infoStyle.Render(fmt.Sprintf("  mic: %s", m.opts.Device))
	outLine := infoStyle.Render(fmt.Sprintf("  out: %s", m.opts.OutputPath))

	// Keys
	keys := dimStyle.Render("  [p]ause  [q]uit  [Q]uit+transcribe  [d]evices")

	return lipgloss.JoinVertical(lipgloss.Left,
		header, "", center, "", micLine, outLine, "", keys,
	)
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func channelStr(c int) string {
	if c == 1 {
		return "mono"
	}
	return "stereo"
}
