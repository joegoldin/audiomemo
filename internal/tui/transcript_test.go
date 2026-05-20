package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestTranscriptViewportAutoScrollDefault(t *testing.T) {
	tv := NewTranscriptViewport(80, 24)
	if !tv.IsAutoScroll() {
		t.Error("expected autoScroll to be true by default")
	}
}

func TestTranscriptViewportAppendCommitted(t *testing.T) {
	tv := NewTranscriptViewport(80, 24)
	tv.AppendCommitted("hello world")

	view := tv.viewport.View()
	if !strings.Contains(view, "hello world") {
		t.Errorf("expected view to contain 'hello world', got: %q", view)
	}
}

func TestTranscriptViewportPartialText(t *testing.T) {
	tv := NewTranscriptViewport(80, 24)
	tv.SetPartial("typing now")

	// The partial text is set in viewport content (dim styled).
	// Check that the raw content (via viewport) contains the partial text.
	view := tv.viewport.View()
	if !strings.Contains(view, "typing now") {
		t.Errorf("expected view to contain 'typing now', got: %q", view)
	}
}

func TestTranscriptViewportWordWrap(t *testing.T) {
	tv := NewTranscriptViewport(20, 24)
	tv.AppendCommitted("one two three four five six")

	wrapped := wrapTranscript("one two three four five six", "", 20)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Errorf("expected wrapping to produce multiple lines, got: %q", wrapped)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > 20 {
			t.Errorf("line %d exceeds width 20 (width=%d): %q", i, w, line)
		}
	}
}

func TestWrapTranscriptWrapsPartialOverflow(t *testing.T) {
	// Committed text fills part of a line; partial extends well past width.
	// Every output line must fit the width — including the line where the
	// partial begins after committed text.
	const width = 30
	committed := "done text near edge here"
	partial := "now we keep typing more words that absolutely must wrap to next lines"
	out := wrapTranscript(committed, partial, width)
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line %d width %d exceeds %d: %q", i, w, width, line)
		}
	}
}

func TestWrapTranscriptDimsPartialWords(t *testing.T) {
	out := wrapTranscript("done", "wip", 80)
	dimmed := transcriptDimStyle.Render("wip")
	if !strings.Contains(out, dimmed) {
		t.Errorf("expected dim-styled %q in output, got: %q", dimmed, out)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("expected committed %q in output, got: %q", "done", out)
	}
}

func TestWrapTranscriptHandlesOnlyPartial(t *testing.T) {
	out := wrapTranscript("", "alpha beta gamma", 8)
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 8 {
			t.Errorf("line %d width %d exceeds 8: %q", i, w, line)
		}
	}
	for _, w := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(out, w) {
			t.Errorf("expected %q in output, got: %q", w, out)
		}
	}
}

func TestTranscriptViewportClearsPartialOnCommit(t *testing.T) {
	tv := NewTranscriptViewport(80, 24)
	tv.SetPartial("hello")

	if tv.partial != "hello" {
		t.Errorf("expected partial to be 'hello', got %q", tv.partial)
	}

	tv.AppendCommitted("hello")

	if tv.partial != "" {
		t.Errorf("expected partial to be cleared after AppendCommitted, got %q", tv.partial)
	}
}
