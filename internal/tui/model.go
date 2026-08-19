package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joegoldin/audiomemo/internal/record"
	"github.com/joegoldin/audiomemo/internal/transcribe"
)

type State int

const (
	StateRecording State = iota
	StateReady
	StateSaved
)

// StartFunc creates and starts a new Recorder for a deferred clip, optionally
// with a live-transcription Streamer. The string is a stream-unavailable note
// ("" when streaming started) rendered dim below the transcript.
type StartFunc func() (*record.Recorder, *transcribe.Streamer, string, error)

type Model struct {
	state        State
	recorder     *record.Recorder
	opts         record.RecordOpts
	startTime    time.Time
	elapsed      time.Duration
	level        float64
	vu           VUMeter
	transcribe   bool // set when user presses Q to quit-and-transcribe
	muted        bool
	clipDone     bool // set when user presses q in clips mode (save clip, continue)
	clipsMode    bool
	clipNumber   int
	savedMessage string // e.g. "Saved clip 3!"
	startFunc    StartFunc
	err          error
	width        int
	height       int
	streamer     *transcribe.Streamer
	transcript   TranscriptViewport
	streamErr    error
	streamNote   string // e.g. "live transcription unavailable: ..."
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

// StreamErr returns any error from the live transcription stream.
func (m *Model) StreamErr() error {
	return m.streamErr
}

type tickMsg time.Time
type levelMsg float64

// doneMsg reports that the recorder exited. It wraps the error in a struct
// rather than being one: a clean exit carries a nil error, and a nil interface
// is a nil tea.Msg, which bubbletea's event loop discards — the TUI would sit
// there recording nothing after ffmpeg stopped on its own.
type doneMsg struct{ err error }
type committedMsg string
type partialMsg string
type streamErrMsg error

func NewModel(rec *record.Recorder, opts record.RecordOpts) *Model {
	return &Model{
		state:      StateRecording,
		recorder:   rec,
		opts:       opts,
		startTime:  time.Now(),
		level:      -60, // silence floor until the first RMS reading arrives
		transcript: NewTranscriptViewport(60, 10),
	}
}

func NewModelWithStreamer(rec *record.Recorder, opts record.RecordOpts, streamer *transcribe.Streamer) *Model {
	m := NewModel(rec, opts)
	m.streamer = streamer
	return m
}

// NewClipsModel creates a Model for clips mode. If rec is nil, starts in StateReady
// and uses startFunc to create the recorder (and streamer) when the user presses space/m.
func NewClipsModel(startFunc StartFunc, rec *record.Recorder, streamer *transcribe.Streamer, opts record.RecordOpts, clipNumber int, savedMessage string) *Model {
	initialState := StateRecording
	if rec == nil {
		initialState = StateReady
	}
	return &Model{
		state:        initialState,
		recorder:     rec,
		streamer:     streamer,
		opts:         opts,
		startTime:    time.Now(),
		level:        -60, // silence floor until the first RMS reading arrives
		transcript:   NewTranscriptViewport(60, 10),
		clipsMode:    true,
		clipNumber:   clipNumber,
		savedMessage: savedMessage,
		startFunc:    startFunc,
	}
}

// SetStreamNote sets a dim informational note shown below the transcript,
// e.g. "live transcription unavailable: no ElevenLabs API key configured".
func (m *Model) SetStreamNote(note string) {
	m.streamNote = note
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tickCmd()}
	if m.state == StateRecording {
		cmds = append(cmds, listenLevel(m.recorder), listenDone(m.recorder))
	}
	if m.streamer != nil {
		cmds = append(cmds, listenCommitted(m.streamer), listenPartial(m.streamer), listenStreamErr(m.streamer))
	}
	if m.state == StateReady {
		return cmds[0] // just tickCmd for ready state
	}
	return tea.Batch(cmds...)
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
		return doneMsg{err: err}
	}
}

func listenCommitted(s *transcribe.Streamer) tea.Cmd {
	return func() tea.Msg {
		text, ok := <-s.Committed
		if !ok {
			return nil
		}
		return committedMsg(text)
	}
}

func listenPartial(s *transcribe.Streamer) tea.Cmd {
	return func() tea.Msg {
		text, ok := <-s.Partial
		if !ok {
			return nil
		}
		return partialMsg(text)
	}
}

func listenStreamErr(s *transcribe.Streamer) tea.Cmd {
	return func() tea.Msg {
		err, ok := <-s.Err
		if !ok {
			return nil
		}
		return streamErrMsg(err)
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// header + mic + out + 2 separators + keys = 6 fixed rows, plus up to
		// 2 optional rows (saved message, stream error/note).
		viewportHeight := msg.Height - 8
		if viewportHeight < 4 {
			viewportHeight = 4
		}
		m.transcript.SetSize(msg.Width, viewportHeight)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tickMsg:
		if m.state == StateRecording {
			m.elapsed = time.Since(m.startTime)
		}
		if m.state == StateRecording && !m.muted {
			m.vu.Push(dbToLevel(m.level))
		} else {
			m.vu.Push(0)
		}
		paused := m.muted || m.state != StateRecording
		m.transcript.SetCursor(renderVUCursor(m.vu.Level(), paused))
		return m, tickCmd()

	case levelMsg:
		m.level = float64(msg)
		return m, listenLevel(m.recorder)

	case doneMsg:
		m.state = StateSaved
		if msg.err != nil {
			m.err = msg.err
		}
		return m, tea.Quit

	case committedMsg:
		m.transcript.AppendCommitted(string(msg))
		return m, listenCommitted(m.streamer)

	case partialMsg:
		m.transcript.SetPartial(string(msg))
		return m, listenPartial(m.streamer)

	case streamErrMsg:
		// Keep liveTranscription true so the transcript captured before the
		// failure stays visible. The error line below the transcript informs
		// the user that streaming stopped; recording continues unaffected.
		m.streamErr = error(msg)
		return m, nil
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg.String() {
	case "up", "down", "pgup", "pgdown", "end":
		m.transcript, cmd = m.transcript.Update(msg)
		return m, cmd
	}

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
				rec, streamer, note, err := m.startFunc()
				if err != nil {
					m.err = err
					return m, tea.Quit
				}
				m.recorder = rec
				m.streamer = streamer
				m.streamNote = note
			}
			m.state = StateRecording
			m.startTime = time.Now()
			m.elapsed = 0
			m.savedMessage = ""
			m.muted = false
			cmds := []tea.Cmd{listenLevel(m.recorder), listenDone(m.recorder)}
			if m.streamer != nil {
				cmds = append(cmds, listenCommitted(m.streamer), listenPartial(m.streamer), listenStreamErr(m.streamer))
			}
			return m, tea.Batch(cmds...)
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
	recStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Bold(true)
	readyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308")).Bold(true)
	muteStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308")).Bold(true)
	savedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e")).Bold(true)
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	infoStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#a1a1aa"))
	streamErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true)
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
	var clipInfo string
	if m.clipsMode {
		clipInfo = dimStyle.Render(fmt.Sprintf("  clip %d", m.clipNumber))
	}
	left := fmt.Sprintf("  %s  %s%s", status, dur, clipInfo)
	info := dimStyle.Render(fmt.Sprintf("%dkHz %s", m.opts.SampleRate/1000, channelStr(m.opts.Channels)))
	right := info + vuDBText.Render("  "+formatDB(m.vu.Level())+"  ")
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 2 {
		pad = 2
	}
	header := left + strings.Repeat(" ", pad) + right

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
			keys = dimStyle.Render("  [↑↓] scroll  [m]ute  [q] save clip  [Q]uit+transcribe")
		}
	} else {
		keys = dimStyle.Render("  [↑↓] scroll  [m]ute  [q]uit  [Q]uit+transcribe")
	}

	sepWidth := m.width
	if sepWidth <= 0 {
		sepWidth = 60
	}
	sep := dimStyle.Render(strings.Repeat("─", sepWidth))

	parts := []string{header, micLine, outLine}
	if savedLine != "" {
		parts = append(parts, savedLine)
	}
	parts = append(parts, sep, m.transcript.View())
	if m.streamErr != nil {
		parts = append(parts, streamErrStyle.Render(fmt.Sprintf("  ⚠ live transcription stopped: %v", m.streamErr)))
	} else if m.streamNote != "" {
		parts = append(parts, dimStyle.Render("  "+m.streamNote))
	}
	parts = append(parts, sep, keys)

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
