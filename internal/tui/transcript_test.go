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

	wrapped := wrapTranscript("one two three four five six", "", 20, "")
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
	out := wrapTranscript(committed, partial, width, "")
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line %d width %d exceeds %d: %q", i, w, width, line)
		}
	}
}

func TestWrapTranscriptDimsPartialWords(t *testing.T) {
	out := wrapTranscript("done", "wip", 80, "")
	dimmed := transcriptDimStyle.Render("wip")
	if !strings.Contains(out, dimmed) {
		t.Errorf("expected dim-styled %q in output, got: %q", dimmed, out)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("expected committed %q in output, got: %q", "done", out)
	}
}

func TestWrapTranscriptHandlesOnlyPartial(t *testing.T) {
	out := wrapTranscript("", "alpha beta gamma", 8, "")
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

func TestTranscriptViewportSetPartialAutoScrolls(t *testing.T) {
	// Partial text that wraps past the viewport height must scroll into view
	// without waiting for committed text.
	tv := NewTranscriptViewport(10, 2)
	tv.AppendCommitted("one two three four")
	tv.SetPartial("five six seven eight nine ten eleven twelve")

	if !tv.viewport.AtBottom() {
		t.Error("expected viewport at bottom after SetPartial with autoScroll on")
	}
}

func TestTranscriptViewportSetPartialRespectsManualScroll(t *testing.T) {
	tv := NewTranscriptViewport(10, 2)
	tv.AppendCommitted("one two three four five six seven eight")
	tv.autoScroll = false
	tv.viewport.GotoTop()

	tv.SetPartial("nine ten eleven twelve thirteen")

	if tv.viewport.AtBottom() {
		t.Error("expected viewport to stay put after SetPartial with autoScroll off")
	}
}

func TestWrapTranscriptAppendsCursor(t *testing.T) {
	cursor := "▅"
	out := wrapTranscript("done", "typing", 80, cursor)
	if !strings.HasSuffix(out, cursor) {
		t.Errorf("expected output to end with cursor, got: %q", out)
	}
	lines := strings.Split(out, "\n")
	last := lines[len(lines)-1]
	if last == cursor {
		t.Errorf("cursor must not sit alone on its own line: %q", out)
	}
}

func TestWrapTranscriptCursorOnEmpty(t *testing.T) {
	cursor := "▁"
	if out := wrapTranscript("", "", 80, cursor); out != cursor {
		t.Errorf("empty transcript should render just the cursor, got: %q", out)
	}
	if out := wrapTranscript("", "", 80, ""); out != "" {
		t.Errorf("empty transcript with no cursor should be empty, got: %q", out)
	}
}

func TestWrapTranscriptCursorReservesCell(t *testing.T) {
	// "one two" is exactly 7 wide. With width 7 the cursor cell doesn't fit
	// after "two", so "two"+cursor wrap to the next line together.
	cursor := "▃"
	out := wrapTranscript("one two", "", 7, cursor)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	if lines[0] != "one" {
		t.Errorf("expected first line 'one', got %q", lines[0])
	}
	if lines[1] != "two"+cursor {
		t.Errorf("expected last line 'two'+cursor, got %q", lines[1])
	}
}

func TestWrapTranscriptNoReservationWithoutCursor(t *testing.T) {
	// Without a cursor the same text fits on one line — no behavior change.
	out := wrapTranscript("one two", "", 7, "")
	if out != "one two" {
		t.Errorf("expected single line 'one two', got %q", out)
	}
}

func TestWrapTranscriptCursorAfterDimPartial(t *testing.T) {
	cursor := "▅"
	out := wrapTranscript("done", "wip", 80, cursor)
	want := "done " + transcriptDimStyle.Render("wip") + cursor
	if out != want {
		t.Errorf("expected %q, got %q", want, out)
	}
}

func TestTranscriptViewportSetCursor(t *testing.T) {
	tv := NewTranscriptViewport(80, 24)
	tv.AppendCommitted("hello")
	tv.SetCursor("▅")
	if view := tv.viewport.View(); !strings.Contains(view, "▅") {
		t.Errorf("expected viewport content to contain cursor, got: %q", view)
	}
	tv.SetCursor("█")
	if view := tv.viewport.View(); !strings.Contains(view, "█") {
		t.Errorf("expected viewport content to update cursor, got: %q", view)
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
