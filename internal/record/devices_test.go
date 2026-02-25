package record

import (
	"testing"
)

func sampleDevices() []Device {
	return []Device{
		{Name: "alsa_output.pci-0000_00_1f.3.analog-stereo.monitor", Description: "Monitor of Built-in Audio Analog Stereo", IsDefault: true, IsMonitor: true},
		{Name: "alsa_input.pci-0000_00_1f.3.analog-stereo", Description: "Built-in Audio Analog Stereo"},
		{Name: "alsa_input.usb-MOTU_M2_M20000044767-00.HiFi__Mic1__source", Description: "M Series Mic In 1L"},
	}
}

func TestResolveDeviceNameRaw(t *testing.T) {
	devices := sampleDevices()
	got := ResolveDeviceName("alsa_input.usb-MOTU_M2_M20000044767-00.HiFi__Mic1__source", devices)
	if got != "alsa_input.usb-MOTU_M2_M20000044767-00.HiFi__Mic1__source" {
		t.Errorf("expected raw name unchanged, got %s", got)
	}
}

func TestResolveDeviceNamePretty(t *testing.T) {
	devices := sampleDevices()
	got := ResolveDeviceName("M Series Mic In 1L", devices)
	if got != "alsa_input.usb-MOTU_M2_M20000044767-00.HiFi__Mic1__source" {
		t.Errorf("expected raw name from pretty name, got %s", got)
	}
}

func TestResolveDeviceNameUnknown(t *testing.T) {
	devices := sampleDevices()
	got := ResolveDeviceName("some-unknown-device", devices)
	if got != "some-unknown-device" {
		t.Errorf("expected unknown name returned as-is, got %s", got)
	}
}

func TestResolveDeviceNames(t *testing.T) {
	devices := sampleDevices()
	names := []string{
		"M Series Mic In 1L",
		"alsa_output.pci-0000_00_1f.3.analog-stereo.monitor",
		"unknown-device",
	}
	got := ResolveDeviceNames(names, devices)
	expected := []string{
		"alsa_input.usb-MOTU_M2_M20000044767-00.HiFi__Mic1__source",
		"alsa_output.pci-0000_00_1f.3.analog-stereo.monitor",
		"unknown-device",
	}
	for i, g := range got {
		if g != expected[i] {
			t.Errorf("index %d: expected %s, got %s", i, expected[i], g)
		}
	}
}

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
