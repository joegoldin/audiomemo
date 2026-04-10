package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var transcriptDimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

// TranscriptViewport displays scrollable transcript text with auto-scroll.
type TranscriptViewport struct {
	viewport   viewport.Model
	committed  string // accumulated committed text
	partial    string // current partial text (shown dim)
	autoScroll bool
	width      int
	height     int
}

// NewTranscriptViewport creates a new TranscriptViewport with the given dimensions.
func NewTranscriptViewport(width, height int) TranscriptViewport {
	vp := viewport.New(width, height)
	return TranscriptViewport{
		viewport:   vp,
		autoScroll: true,
		width:      width,
		height:     height,
	}
}

// AppendCommitted appends text to the committed transcript, clears partial,
// rebuilds viewport content, and scrolls to bottom if autoScroll is enabled.
func (t *TranscriptViewport) AppendCommitted(text string) {
	if t.committed == "" {
		t.committed = text
	} else {
		t.committed = t.committed + " " + text
	}
	t.partial = ""
	t.rebuildContent()
	if t.autoScroll {
		t.viewport.GotoBottom()
	}
}

// SetPartial sets the current partial (in-progress) text shown in dim style.
// Does not change the scroll position.
func (t *TranscriptViewport) SetPartial(text string) {
	t.partial = text
	t.rebuildContent()
}

// SetSize updates the viewport dimensions and rebuilds content.
func (t *TranscriptViewport) SetSize(width, height int) {
	t.width = width
	t.height = height
	t.viewport.Width = width
	t.viewport.Height = height
	t.rebuildContent()
}

// Update delegates to the underlying viewport and manages auto-scroll state.
func (t TranscriptViewport) Update(msg tea.Msg) (TranscriptViewport, tea.Cmd) {
	var cmd tea.Cmd
	t.viewport, cmd = t.viewport.Update(msg)

	// Re-engage auto-scroll if we've scrolled to the bottom.
	if t.viewport.AtBottom() {
		t.autoScroll = true
	}

	// Disengage auto-scroll on upward scroll key presses.
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "up", "pgup", "k", "u", "ctrl+u", "b":
			t.autoScroll = false
		}
	}

	return t, cmd
}

// View renders the viewport. If not at the bottom and there's content,
// overlays a "↓ live" indicator at the bottom-right.
func (t TranscriptViewport) View() string {
	view := t.viewport.View()
	if !t.autoScroll && t.height > 0 {
		indicator := transcriptDimStyle.Render("↓ live")
		lines := strings.Split(view, "\n")
		if len(lines) > 0 {
			lastIdx := len(lines) - 1
			lastLine := lines[lastIdx]
			// Measure visible width of last line (strip ANSI for padding calc).
			// Use simple padding to right-align.
			padWidth := t.width - lipgloss.Width(lastLine) - lipgloss.Width(indicator)
			if padWidth < 0 {
				padWidth = 0
			}
			lines[lastIdx] = lastLine + strings.Repeat(" ", padWidth) + indicator
			view = strings.Join(lines, "\n")
		}
	}
	return view
}

// IsAutoScroll returns whether auto-scroll is currently engaged.
func (t TranscriptViewport) IsAutoScroll() bool {
	return t.autoScroll
}

// rebuildContent rebuilds the viewport content from committed + partial text.
func (t *TranscriptViewport) rebuildContent() {
	wrapped := wordWrap(t.committed, t.width)
	if t.partial != "" {
		if wrapped != "" {
			wrapped += " "
		}
		wrapped += transcriptDimStyle.Render(t.partial)
	}
	t.viewport.SetContent(wrapped)
}

// wordWrap wraps text at the given width by breaking on spaces.
// Words are never broken; if a word is longer than width it stays on its own line.
func wordWrap(text string, width int) string {
	if width <= 0 || text == "" {
		return text
	}

	words := strings.Split(text, " ")
	var b strings.Builder
	lineLen := 0

	for i, word := range words {
		if word == "" {
			continue
		}
		wordLen := len(word)
		if i == 0 || lineLen == 0 {
			b.WriteString(word)
			lineLen = wordLen
		} else if lineLen+1+wordLen > width {
			b.WriteByte('\n')
			b.WriteString(word)
			lineLen = wordLen
		} else {
			b.WriteByte(' ')
			b.WriteString(word)
			lineLen += 1 + wordLen
		}
	}

	return b.String()
}
