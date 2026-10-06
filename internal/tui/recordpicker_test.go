package tui

import (
	"errors"
	"slices"
	"testing"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
)

func TestRecordPickerDiscoveryFailure(t *testing.T) {
	cfg := &config.Config{}
	cfg.Record.Device = "mic"
	cfg.Devices = map[string]string{"mic": "old-device"}
	m := &recordPickerModel{state: RPLoading, config: cfg}
	want := errors.New("device discovery failed")
	_, cmd := m.Update(recordPickerErrorMsg{err: want})
	if m.err != want || m.state != RPDone || cmd == nil {
		t.Fatalf("discovery error must quit and reach caller: %+v", m)
	}
	if len(m.items) != 0 {
		t.Fatalf("must not offer saved favorites after failed discovery: %+v", m.items)
	}
}

func TestRecordPickerSelectsMicrophoneAndSystemAudio(t *testing.T) {
	m := &recordPickerModel{state: RPLoading, config: &config.Config{}, selected: map[int]bool{}}
	m.Update(devicesLoadedMsg([]record.Device{
		{Name: "MacBook Pro Microphone", Description: "MacBook Pro Microphone"},
		{Name: record.SystemAudioDevice, Description: "System audio", IsMonitor: true},
	}))
	if len(m.items) != 2 || m.items[1].kind != "monitor" {
		t.Fatalf("system audio must appear alongside hardware inputs: %+v", m.items)
	}
	m.toggleSelect(0)
	m.toggleSelect(1)
	m.finishSelection()
	if len(m.result.Devices) != 2 || !slices.Contains(m.result.Devices, record.SystemAudioDevice) || !slices.Contains(m.result.Devices, "MacBook Pro Microphone") {
		t.Fatalf("expected microphone and system audio selection: %+v", m.result)
	}
}

func TestRecordPickerShowsDiscoveredMacDevices(t *testing.T) {
	cfg := &config.Config{}
	cfg.Record.Device = "mic"
	cfg.Devices = map[string]string{"mic": "old-device"}
	m := &recordPickerModel{state: RPLoading, config: cfg}
	m.Update(devicesLoadedMsg([]record.Device{{Name: "MacBook Pro Microphone", Description: "MacBook Pro Microphone"}}))
	if m.state != RPPick || len(m.items) != 2 {
		t.Fatalf("expected saved favorite and discovered microphone: %+v", m.items)
	}
	m.finishSingle(1)
	if len(m.result.Devices) != 1 || m.result.Devices[0] != "MacBook Pro Microphone" {
		t.Fatalf("selection must use AVFoundation audio name: %+v", m.result)
	}
}
