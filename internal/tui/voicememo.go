package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/joegoldin/audiomemo/internal/voicememo"
)

type voiceMemoModel struct {
	recordings []voicememo.Recording
	matches    []voicememo.Recording
	query      string
	cursor     int
	width      int
	height     int
	selected   *voicememo.Recording
	done       bool
}

func newVoiceMemoModel(recordings []voicememo.Recording) *voiceMemoModel {
	return &voiceMemoModel{recordings: recordings, matches: recordings, width: 80, height: 20}
}
func (m *voiceMemoModel) Init() tea.Cmd { return nil }
func (m *voiceMemoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.done = true
			return m, tea.Quit
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down":
			if m.cursor+1 < len(m.matches) {
				m.cursor++
			}
		case "enter":
			if len(m.matches) > 0 {
				selected := m.matches[m.cursor]
				m.selected = &selected
				m.done = true
				return m, tea.Quit
			}
		case "backspace", "ctrl+h":
			runes := []rune(m.query)
			if len(runes) > 0 {
				m.query = string(runes[:len(runes)-1])
				m.filter()
			}
		case "ctrl+u":
			m.query = ""
			m.filter()
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.query += string(msg.Runes)
				if msg.Type == tea.KeySpace && len(msg.Runes) == 0 {
					m.query += " "
				}
				m.filter()
			}
		}
	}
	return m, nil
}
func (m *voiceMemoModel) filter() {
	m.matches = voicememo.Match(m.recordings, m.query)
	m.cursor = 0
}
func (m *voiceMemoModel) View() string {
	if m.done {
		return ""
	}
	var b strings.Builder
	b.WriteString("Voice Memos — select a recording to transcribe\n")
	b.WriteString(ansi.Truncate("Search: "+m.query, max(1, m.width), "…") + "\n\n")
	count := max(1, m.height-6)
	start := max(0, m.cursor-count+1)
	for i := start; i < len(m.matches) && i < start+count; i++ {
		recording := m.matches[i]
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		line := fmt.Sprintf("%s%s  %s  (%s)", cursor, recording.Name, recording.Date.Format("2006-01-02 15:04"), filepath.Base(recording.Path))
		b.WriteString(ansi.Truncate(line, max(1, m.width), "…") + "\n")
	}
	if len(m.matches) == 0 {
		b.WriteString("No matching recordings\n")
	}
	b.WriteString("\nType to search · ↑/↓ move · Enter transcribe · Esc cancel\n")
	return b.String()
}

func RunVoiceMemoPicker(recordings []voicememo.Recording, opts ...tea.ProgramOption) (*voicememo.Recording, error) {
	if len(recordings) == 0 {
		return nil, fmt.Errorf("no Voice Memos to select")
	}
	m := newVoiceMemoModel(recordings)
	_, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return nil, err
	}
	return m.selected, nil
}
