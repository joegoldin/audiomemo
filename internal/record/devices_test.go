package record

import (
	"testing"
)

func TestParseDeviceList(t *testing.T) {
	// Simulated ffmpeg -sources pulse output
	output := `Auto-detected sources for pulse:
  * alsa_output.pci-0000_00_1f.3.analog-stereo.monitor [Monitor of Built-in Audio Analog Stereo]
    alsa_input.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo]
`
	devices := ParseDeviceList(output)
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devices))
	}
	if devices[1].Name != "alsa_input.pci-0000_00_1f.3.analog-stereo" {
		t.Errorf("unexpected device name: %s", devices[1].Name)
	}
	if devices[1].Description != "Built-in Audio Analog Stereo" {
		t.Errorf("unexpected description: %s", devices[1].Description)
	}
	if !devices[0].IsDefault {
		t.Error("expected first device to be default (has *)")
	}
	if !devices[0].IsMonitor {
		t.Error("expected first device (monitor) to have IsMonitor=true")
	}
	if devices[1].IsMonitor {
		t.Error("expected second device (input) to have IsMonitor=false")
	}
}
