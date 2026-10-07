package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegoldin/audiomemo/internal/voicememo"
)

func TestVoiceMemoPicker(t *testing.T) {
	recordings := []voicememo.Recording{{Name: "First", Path: "one.m4a"}, {Name: "Second", Path: "two.m4a"}}
	m := newVoiceMemoModel(recordings)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selected == nil || m.selected.Path != "two.m4a" {
		t.Fatalf("wrong selection: %v", m.selected)
	}
	if m.View() != "" {
		t.Error("finished picker should be hidden")
	}
	m = newVoiceMemoModel(recordings)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second")})
	if len(m.matches) != 1 || !strings.Contains(m.View(), "Second") {
		t.Fatal("search failed")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selected == nil || m.selected.Path != "two.m4a" {
		t.Fatal("filtered selection failed")
	}
}

func TestVoiceMemoPickerCancelAndEmptyFilter(t *testing.T) {
	m := newVoiceMemoModel([]voicememo.Recording{{Name: "First"}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("missing")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.done || m.selected != nil {
		t.Fatal("empty results must not select")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if len(m.matches) != 1 {
		t.Fatal("clear search failed")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.done || m.selected != nil {
		t.Fatal("cancel selected a recording")
	}
}
