package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
)

func TestDevicePreviewIgnoresPreviousCaptureFailure(t *testing.T) {
	previous := make(chan struct{})
	dm := NewDeviceManager(&config.Config{}, "")
	dm.vuCancel = make(chan struct{})
	dm.message = "current device"
	dm.Update(dmVUErrorMsg{err: errors.New("previous device failed"), cancel: previous})
	if dm.message != "current device" {
		t.Fatalf("stale preview failure replaced the current device's message: %q", dm.message)
	}
}

func TestDevicePreviewReportsCaptureFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte("#!/bin/sh\necho 'preview capture failed' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	dm := NewDeviceManager(&config.Config{}, "")
	dm.devices = []record.Device{{Name: "test-microphone"}}
	listen := dm.startVU()
	defer dm.stopVU()
	messages := make(chan tea.Msg, 1)
	go func() { messages <- listen() }()
	select {
	case msg := <-messages:
		dm.Update(msg)
		if !strings.Contains(dm.message, "preview capture failed") {
			t.Fatalf("preview failure must be displayed, got message %q", dm.message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preview failure was not delivered")
	}
}
