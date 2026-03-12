package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joegoldin/audiomemo/internal/record"
)

type State int

const (
	StateRecording State = iota
	StateReady
	StateSaved
)

// StartFunc creates and starts a new Recorder. Used for deferred start in clips mode.
type StartFunc func() (*record.Recorder, error)

type Model struct {
	state        State
	recorder     *record.Recorder
	opts         record.RecordOpts
	startTime    time.Time
	elapsed      time.Duration
	level        float64
	tick         int
	anim         *Animation
	transcribe   bool // set when user presses Q to quit-and-transcribe
	muted        bool
	clipDone     bool   // set when user presses q in clips mode (save clip, continue)
	clipsMode    bool
	clipNumber   int
	savedMessage string // e.g. "Saved clip 3!"
	startFunc    StartFunc
	err          error
	width        int
	height       int
}

// ShouldTranscribe returns true if the user pressed Q to quit-and-transcribe.
func (m *Model) ShouldTranscribe() bool {
	return m.transcribe
}

// ClipDone returns true if the user pressed q in clips mode to save and continue.
func (m *Model) ClipDone() bool {
	return m.clipDone
}

// Recorder returns the underlying recorder (may be nil if never started).
func (m *Model) Recorder() *record.Recorder {
	return m.recorder
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
		anim:      NewAnimation(60, 9),
	}
}

// NewClipsModel creates a Model for clips mode. If rec is nil, starts in StateReady
// and uses startFunc to create the recorder when the user presses space/m.
func NewClipsModel(startFunc StartFunc, rec *record.Recorder, opts record.RecordOpts, clipNumber int, savedMessage string) *Model {
	initialState := StateRecording
	if rec == nil {
		initialState = StateReady
	}
	return &Model{
		state:        initialState,
		recorder:     rec,
		opts:         opts,
		startTime:    time.Now(),
		anim:         NewAnimation(60, 9),
		clipsMode:    true,
		clipNumber:   clipNumber,
		savedMessage: savedMessage,
		startFunc:    startFunc,
	}
}

func (m *Model) Init() tea.Cmd {
	if m.state == StateReady {
		return tickCmd() // tick for UI updates, but no recording yet
	}
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
		return m.handleKey(msg)

	case tickMsg:
		if m.state == StateRecording {
			m.elapsed = time.Since(m.startTime)
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
		if m.state == StateReady {
			return m, tea.Quit
		}
		m.recorder.Stop()
		m.state = StateSaved
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("q"))):
		if m.state == StateReady {
			return m, tea.Quit
		}
		m.recorder.Stop()
		m.state = StateSaved
		if m.clipsMode {
			m.clipDone = true
		}
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("Q"))):
		if m.state == StateReady {
			m.transcribe = true
			return m, tea.Quit
		}
		m.recorder.Stop()
		m.state = StateSaved
		m.transcribe = true
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("m", " "))):
		if m.state == StateReady {
			// Start recording the next clip
			if m.startFunc != nil {
				rec, err := m.startFunc()
				if err != nil {
					m.err = err
					return m, tea.Quit
				}
				m.recorder = rec
			}
			m.state = StateRecording
			m.startTime = time.Now()
			m.elapsed = 0
			m.savedMessage = ""
			m.muted = false
			return m, tea.Batch(listenLevel(m.recorder), listenDone(m.recorder))
		}
		if m.state == StateRecording {
			m.recorder.ToggleMute()
			m.muted = m.recorder.IsMuted()
		}
		return m, nil
	}
	return m, nil
}

var (
	recStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Bold(true)
	readyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308")).Bold(true)
	muteStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308")).Bold(true)
	savedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	infoStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a1a1aa"))
)

func (m *Model) View() string {
	// Status line
	var status string
	switch {
	case m.state == StateSaved:
		status = savedStyle.Render("✓ SAVED")
	case m.state == StateReady:
		status = readyStyle.Render("⏳ READY")
	case m.muted:
		status = muteStyle.Render("🔇 MUTED")
	default:
		status = recStyle.Render("● REC")
	}

	dur := formatDuration(m.elapsed)
	info := fmt.Sprintf("%dkHz %s", m.opts.SampleRate/1000, channelStr(m.opts.Channels))

	var clipInfo string
	if m.clipsMode {
		clipInfo = dimStyle.Render(fmt.Sprintf("  clip %d", m.clipNumber))
	}
	header := fmt.Sprintf("  %s  %s%s       %s", status, dur, clipInfo, dimStyle.Render(info))

	// Waveform (unified VU + scrolling history)
	paused := m.muted || m.state == StateReady
	animLevel := dbToLevel(m.level)
	animView := m.anim.Render(m.tick, animLevel, paused)

	// dB readout to the right of the waveform, vertically centered
	dbStr := vuDBText.Render(" " + formatDB(m.anim.SmoothedLevel()))
	center := lipgloss.JoinHorizontal(lipgloss.Center, animView, dbStr)

	// Info
	micDisplay := m.opts.Device
	if m.opts.DeviceLabel != "" {
		micDisplay = m.opts.DeviceLabel
	}
	micLine := infoStyle.Render(fmt.Sprintf("  mic: %s", micDisplay))
	outLine := infoStyle.Render(fmt.Sprintf("  out: %s", m.opts.OutputPath))

	// Saved message (clips mode, between clips)
	var savedLine string
	if m.savedMessage != "" {
		savedLine = savedStyle.Render(fmt.Sprintf("  ✓ %s", m.savedMessage))
	}

	// Keys
	var keys string
	if m.clipsMode {
		if m.state == StateReady {
			keys = dimStyle.Render("  [space/m] record  [q]uit  [Q]uit+transcribe")
		} else {
			keys = dimStyle.Render("  [m]ute  [q] save clip  [Q]uit+transcribe")
		}
	} else {
		keys = dimStyle.Render("  [m]ute  [q]uit  [Q]uit+transcribe")
	}

	parts := []string{header, "", center, ""}
	if savedLine != "" {
		parts = append(parts, savedLine)
	}
	parts = append(parts, micLine, outLine, "", keys)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
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
