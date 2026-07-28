package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegoldin/audiomemo/internal/record"
)

func testOpts() record.RecordOpts {
	return record.RecordOpts{
		Device:     "default",
		SampleRate: 48000,
		Channels:   2,
		OutputPath: "/tmp/memo.ogg",
	}
}

// advance sends a WindowSizeMsg and one tick so the model has sized its
// viewport and pushed a level into the VU meter.
func advance(m *Model) *Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(*Model)
	next, _ = m.Update(tickMsg(time.Now()))
	return next.(*Model)
}

func TestModelViewShowsVUCursor(t *testing.T) {
	m := advance(NewModel(nil, testOpts()))
	view := m.View()
	// Level 0 while recording → green ▁ floor cursor.
	if !strings.Contains(view, waveGreen.Render("▁")) {
		t.Errorf("expected view to contain the VU cursor, got:\n%s", view)
	}
}

func TestModelViewHasNoWaveformGrid(t *testing.T) {
	m := advance(NewModel(nil, testOpts()))
	view := m.View()
	// The old animation drew scrolling tick marks; the new layout must not.
	if strings.Contains(view, "┊") {
		t.Errorf("expected no waveform tick marks in view, got:\n%s", view)
	}
}

func TestModelViewShowsStreamNote(t *testing.T) {
	m := NewModel(nil, testOpts())
	m.SetStreamNote("live transcription unavailable: no ElevenLabs API key configured")
	m = advance(m)
	if !strings.Contains(m.View(), "live transcription unavailable") {
		t.Error("expected stream note in view")
	}
}

func TestModelViewHeaderHasDB(t *testing.T) {
	m := advance(NewModel(nil, testOpts()))
	lines := strings.Split(m.View(), "\n")
	if !strings.Contains(lines[0], "dB") {
		t.Errorf("expected dB readout in header line, got: %q", lines[0])
	}
	if !strings.Contains(lines[0], "48kHz stereo") {
		t.Errorf("expected sample-rate info in header line, got: %q", lines[0])
	}
}

func TestModelScrollKeysWithoutStreamer(t *testing.T) {
	m := advance(NewModel(nil, testOpts()))
	// Must not panic and must not quit — scroll keys are always active now.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if next == nil {
		t.Fatal("expected model back from scroll key")
	}
	if next.(*Model).state != StateRecording {
		t.Error("scroll key must not change recording state")
	}
}

func TestModelMutedCursorIsDim(t *testing.T) {
	m := advance(NewModel(nil, testOpts()))
	m.muted = true
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(*Model)
	if !strings.Contains(m.View(), vuCursorMutedStyle.Render("▁")) {
		t.Error("expected dim-gray floor cursor while muted")
	}
}

func TestClipsModelReadyKeysHint(t *testing.T) {
	m := NewClipsModel(nil, nil, nil, testOpts(), 2, "Saved clip 1!")
	m = advance(m)
	view := m.View()
	if !strings.Contains(view, "[space/m] record") {
		t.Errorf("expected READY keys hint, got:\n%s", view)
	}
	if !strings.Contains(view, "Saved clip 1!") {
		t.Errorf("expected saved message, got:\n%s", view)
	}
}
