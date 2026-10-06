//go:build darwin && cgo

package tui

import (
	"os"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
)

func TestNativeSystemAudioPreview(t *testing.T) {
	if os.Getenv("AUDIOMEMO_TEST_SYSTEM_AUDIO") != "1" {
		t.Skip("set AUDIOMEMO_TEST_SYSTEM_AUDIO=1 to test real Core Audio capture")
	}
	dm := NewDeviceManager(&config.Config{}, "")
	dm.devices = []record.Device{{Name: record.SystemAudioDevice, IsMonitor: true}}
	dm.startVU()
	levels := dm.vuMsgCh
	defer dm.stopVU()
	select {
	case msg, ok := <-levels:
		if !ok {
			t.Fatal("system audio preview exited without a level")
		}
		if _, ok := msg.(dmVUMsg); !ok {
			t.Fatalf("expected preview level, got %+v", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("system audio preview did not produce levels")
	}
	dm.stopVU()
	finished := make(chan struct{})
	go func() {
		for range levels {
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("system audio preview did not release capture")
	}
}
