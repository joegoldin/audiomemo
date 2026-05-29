package record

import (
	"strings"
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

// Realistic device snapshot from a workstation: MOTU M2 + miniDSP + USB DAC +
// laptop docks. Used to exercise FuzzyMatchDevice against actual drift cases.
func driftScenarioDevices() []Device {
	return []Device{
		{Name: "alsa_input.usb-MOTU_M2_M20000044767-00.HiFi__Mic1__source", Description: "M Series Mic In 1L"},
		{Name: "alsa_output.usb-MOTU_M2_M20000044767-00.HiFi__Line__sink", Description: "Motu Line Out"},
		{Name: "alsa_output.usb-MOTU_M2_M20000044767-00.HiFi__Line__sink.monitor", Description: "Monitor of Motu Line Out", IsMonitor: true},
		{Name: "alsa_output.usb-Generic_USB_Audio-00.HiFi__SPDIF__sink.monitor", Description: "Monitor of USB Audio S/PDIF Output", IsMonitor: true},
		{Name: "alsa_output.usb-Generic_USB_Audio-00.HiFi__Speaker__sink.monitor", Description: "Monitor of USB Audio Speakers", IsMonitor: true},
		{Name: "alsa_output.usb-Generic_USB_Audio-00.HiFi__Headphones__sink.monitor", Description: "Monitor of USB Audio Front Headphone", IsMonitor: true},
		{Name: "alsa_input.usb-Generic_USB_Audio-00.HiFi__Mic__source", Description: "USB Audio Microphone"},
		{Name: "alsa_input.usb-Generic_USB_Audio-00.HiFi__Line__source", Description: "USB Audio Line Input"},
		{Name: "alsa_input.usb-miniDSP_miniDSP_2x4HD-00.analog-surround-40", Description: "miniDSP 2x4HD Analog Surround 4.0"},
		{Name: "alsa_output.usb-miniDSP_miniDSP_2x4HD-00.analog-stereo.monitor", Description: "Monitor of miniDSP 2x4HD Analog Stereo", IsMonitor: true},
		{Name: "alsa_output.usb-QTIL_Qudelix-5K_USB_DAC_96KHz_ABCDEF0123456789-00.analog-stereo.monitor", Description: "Monitor of Qudelix-5K USB DAC", IsMonitor: true},
	}
}

func TestFuzzyMatchDeviceDrift(t *testing.T) {
	// PulseAudio renamed "HiFi__Line1__sink" -> "HiFi__Line__sink" after a
	// profile change; the stored config still references Line1.
	stale := "alsa_output.usb-MOTU_M2_M20000044767-00.HiFi__Line1__sink.monitor"
	matched, ok := FuzzyMatchDevice(stale, driftScenarioDevices())
	if !ok {
		t.Fatal("expected fuzzy match for drifted MOTU sink monitor")
	}
	want := "alsa_output.usb-MOTU_M2_M20000044767-00.HiFi__Line__sink.monitor"
	if matched.Name != want {
		t.Errorf("expected %s, got %s", want, matched.Name)
	}
}

func TestFuzzyMatchDeviceRejectsUnrelated(t *testing.T) {
	// A name that shares only the protocol prefix should not match anything.
	if _, ok := FuzzyMatchDevice("alsa_output.unrelated_device.monitor", driftScenarioDevices()); ok {
		t.Error("expected no fuzzy match for unrelated device")
	}
}

func TestFuzzyMatchDeviceRespectsDirection(t *testing.T) {
	// A configured input with the MOTU serial should never be substituted with
	// the MOTU output sink, even if tokens overlap heavily.
	stale := "alsa_input.usb-MOTU_M2_M20000044767-00.HiFi__MicFoo__source"
	matched, ok := FuzzyMatchDevice(stale, driftScenarioDevices())
	if !ok {
		t.Fatal("expected fuzzy match to find the MOTU mic source")
	}
	if !strings.HasPrefix(matched.Name, "alsa_input") {
		t.Errorf("fuzzy match crossed direction boundary: %s", matched.Name)
	}
}

func TestFuzzyMatchDeviceRequiresMonitorAgreement(t *testing.T) {
	// A configured monitor must not be substituted with a non-monitor sink.
	devices := []Device{
		{Name: "alsa_output.usb-MOTU_M2_M20000044767-00.HiFi__Line__sink", Description: "Motu Line Out"},
	}
	stale := "alsa_output.usb-MOTU_M2_M20000044767-00.HiFi__Line1__sink.monitor"
	if _, ok := FuzzyMatchDevice(stale, devices); ok {
		t.Error("expected fuzzy match to reject non-monitor candidate for monitor target")
	}
}

func TestFuzzyMatchDeviceAmbiguousReject(t *testing.T) {
	// Two equally-close candidates should be rejected to avoid silent miswire.
	devices := []Device{
		{Name: "alsa_output.usb-foo-00.HiFi__A__sink.monitor", IsMonitor: true},
		{Name: "alsa_output.usb-foo-00.HiFi__B__sink.monitor", IsMonitor: true},
	}
	stale := "alsa_output.usb-foo-00.HiFi__C__sink.monitor"
	if _, ok := FuzzyMatchDevice(stale, devices); ok {
		t.Error("expected fuzzy match to reject ambiguous candidates")
	}
}

func TestHasDevice(t *testing.T) {
	devices := driftScenarioDevices()
	if !HasDevice("alsa_input.usb-MOTU_M2_M20000044767-00.HiFi__Mic1__source", devices) {
		t.Error("expected HasDevice true for known mic")
	}
	if HasDevice("alsa_input.does-not-exist", devices) {
		t.Error("expected HasDevice false for missing device")
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
