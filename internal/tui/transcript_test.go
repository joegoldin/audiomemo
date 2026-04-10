package tui

import (
	"strings"
	"testing"
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
	// Append a string longer than 20 chars so wrapping occurs.
	tv.AppendCommitted("one two three four five six")

	// Get the raw content from the viewport by rebuilding it.
	wrapped := wordWrap("one two three four five six", 20)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Errorf("expected wrapping to produce multiple lines, got: %q", wrapped)
	}
	// Verify no line exceeds width 20.
	for i, line := range lines {
		if len(line) > 20 {
			t.Errorf("line %d exceeds width 20: %q (len=%d)", i, line, len(line))
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
