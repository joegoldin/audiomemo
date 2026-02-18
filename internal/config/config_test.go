package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := Default()
	if cfg.Record.Format != "ogg" {
		t.Errorf("expected default format ogg, got %s", cfg.Record.Format)
	}
	if cfg.Record.SampleRate != 48000 {
		t.Errorf("expected default sample rate 48000, got %d", cfg.Record.SampleRate)
	}
	if cfg.Record.Channels != 1 {
		t.Errorf("expected default channels 1, got %d", cfg.Record.Channels)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`
[record]
format = "wav"
sample_rate = 44100

[transcribe]
default_backend = "deepgram"

[transcribe.deepgram]
api_key = "test-key"
model = "nova-2"
`), 0644)

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Record.Format != "wav" {
		t.Errorf("expected wav, got %s", cfg.Record.Format)
	}
	if cfg.Record.SampleRate != 44100 {
		t.Errorf("expected 44100, got %d", cfg.Record.SampleRate)
	}
	if cfg.Record.Channels != 1 {
		t.Errorf("expected default channels 1, got %d", cfg.Record.Channels)
	}
	if cfg.Transcribe.DefaultBackend != "deepgram" {
		t.Errorf("expected deepgram, got %s", cfg.Transcribe.DefaultBackend)
	}
	if cfg.Transcribe.Deepgram.APIKey != "test-key" {
		t.Errorf("expected test-key, got %s", cfg.Transcribe.Deepgram.APIKey)
	}
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	cfg, err := LoadFrom("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Record.Format != "ogg" {
		t.Errorf("expected default ogg, got %s", cfg.Record.Format)
	}
}

func TestEnvVarOverridesConfig(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "env-key")
	cfg := Default()
	cfg.ApplyEnv()
	if cfg.Transcribe.Deepgram.APIKey != "env-key" {
		t.Errorf("expected env-key, got %s", cfg.Transcribe.Deepgram.APIKey)
	}
}

func TestResolveOutputDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg := Default()
	dir := cfg.ResolveOutputDir()
	expected := filepath.Join(home, "Recordings")
	if dir != expected {
		t.Errorf("expected %s, got %s", expected, dir)
	}
}

func TestDefaultMapsInitialized(t *testing.T) {
	cfg := Default()
	if cfg.Devices == nil {
		t.Error("expected Devices map to be non-nil")
	}
	if cfg.DeviceGroups == nil {
		t.Error("expected DeviceGroups map to be non-nil")
	}
	if len(cfg.Devices) != 0 {
		t.Errorf("expected empty Devices map, got %d entries", len(cfg.Devices))
	}
	if len(cfg.DeviceGroups) != 0 {
		t.Errorf("expected empty DeviceGroups map, got %d entries", len(cfg.DeviceGroups))
	}
}

func TestSaveToAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := Default()
	cfg.Record.Format = "wav"
	cfg.Record.SampleRate = 44100
	cfg.Record.Device = "mic"
	cfg.Devices["mic"] = "alsa_input.usb-Blue_Microphones-00.mono-fallback"
	cfg.Devices["desktop"] = "alsa_output.pci-0000_0c_00.4.analog-stereo.monitor"
	cfg.DeviceGroups["zoom"] = []string{"mic", "desktop"}
	cfg.Transcribe.DefaultBackend = "deepgram"
	cfg.Transcribe.Deepgram.APIKey = "test-key"

	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if loaded.Record.Format != "wav" {
		t.Errorf("expected format wav, got %s", loaded.Record.Format)
	}
	if loaded.Record.SampleRate != 44100 {
		t.Errorf("expected sample_rate 44100, got %d", loaded.Record.SampleRate)
	}
	if loaded.Record.Device != "mic" {
		t.Errorf("expected device mic, got %s", loaded.Record.Device)
	}
	if loaded.Devices["mic"] != "alsa_input.usb-Blue_Microphones-00.mono-fallback" {
		t.Errorf("expected mic alias, got %s", loaded.Devices["mic"])
	}
	if loaded.Devices["desktop"] != "alsa_output.pci-0000_0c_00.4.analog-stereo.monitor" {
		t.Errorf("expected desktop alias, got %s", loaded.Devices["desktop"])
	}
	if !reflect.DeepEqual(loaded.DeviceGroups["zoom"], []string{"mic", "desktop"}) {
		t.Errorf("expected zoom group [mic desktop], got %v", loaded.DeviceGroups["zoom"])
	}
	if loaded.Transcribe.DefaultBackend != "deepgram" {
		t.Errorf("expected deepgram backend, got %s", loaded.Transcribe.DefaultBackend)
	}
	if loaded.Transcribe.Deepgram.APIKey != "test-key" {
		t.Errorf("expected test-key, got %s", loaded.Transcribe.Deepgram.APIKey)
	}
}

func TestSaveToCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dir", "config.toml")

	cfg := Default()
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo failed to create nested directories: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file was not created: %v", err)
	}
}

func TestSaveDefaultRoundTrip(t *testing.T) {
	// Save a default config, load it back, and verify all defaults are preserved.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	original := Default()
	if err := original.SaveTo(path); err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if !reflect.DeepEqual(original, loaded) {
		t.Errorf("round-tripped config does not match original.\nOriginal: %+v\nLoaded:   %+v", original, loaded)
	}
}

func TestSaveUsesXDGPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := Default()
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	expectedPath := filepath.Join(dir, "audiotools", "config.toml")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("config file not found at expected XDG path %s: %v", expectedPath, err)
	}

	loaded, err := LoadFrom(expectedPath)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if !reflect.DeepEqual(cfg, loaded) {
		t.Error("round-tripped config via Save() does not match original")
	}
}

func TestResolveDeviceAlias(t *testing.T) {
	cfg := Default()
	cfg.Devices["mic"] = "alsa_input.usb-Blue_Microphones-00.mono-fallback"

	result, err := cfg.ResolveDevice("mic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"alsa_input.usb-Blue_Microphones-00.mono-fallback"}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestResolveDeviceGroup(t *testing.T) {
	cfg := Default()
	cfg.Devices["mic"] = "alsa_input.usb-Blue_Microphones-00.mono-fallback"
	cfg.Devices["desktop"] = "alsa_output.pci-0000_0c_00.4.analog-stereo.monitor"
	cfg.DeviceGroups["zoom"] = []string{"mic", "desktop"}

	result, err := cfg.ResolveDevice("zoom")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{
		"alsa_input.usb-Blue_Microphones-00.mono-fallback",
		"alsa_output.pci-0000_0c_00.4.analog-stereo.monitor",
	}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestResolveDeviceRawName(t *testing.T) {
	cfg := Default()

	result, err := cfg.ResolveDevice("alsa_input.usb-some-device")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"alsa_input.usb-some-device"}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestResolveDeviceEmptyName(t *testing.T) {
	cfg := Default()

	result, err := cfg.ResolveDevice("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"default"}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestResolveDeviceGroupMissingAlias(t *testing.T) {
	cfg := Default()
	cfg.Devices["mic"] = "alsa_input.usb-Blue_Microphones-00.mono-fallback"
	cfg.DeviceGroups["broken"] = []string{"mic", "nonexistent"}

	_, err := cfg.ResolveDevice("broken")
	if err == nil {
		t.Fatal("expected error for missing alias in group, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention the missing alias 'nonexistent', got: %v", err)
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error should mention the group name 'broken', got: %v", err)
	}
}

func TestResolveDeviceGroupPriorityOverAlias(t *testing.T) {
	// If a name matches both a group and an alias, the group takes priority.
	cfg := Default()
	cfg.Devices["both"] = "raw-alias-device"
	cfg.DeviceGroups["both"] = []string{"both"}

	result, err := cfg.ResolveDevice("both")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Group resolution: "both" group -> alias "both" -> "raw-alias-device"
	expected := []string{"raw-alias-device"}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("expected group resolution %v, got %v", expected, result)
	}
}

func TestNeedsOnboardingNewConfig(t *testing.T) {
	cfg := Default()
	if !cfg.NeedsOnboarding() {
		t.Error("expected new default config to need onboarding")
	}
}

func TestNeedsOnboardingCompleted(t *testing.T) {
	cfg := Default()
	cfg.OnboardVersion = CurrentOnboardVersion
	if cfg.NeedsOnboarding() {
		t.Error("expected config with current OnboardVersion to skip onboarding")
	}
}

func TestNeedsOnboardingExistingSetup(t *testing.T) {
	cfg := Default()
	// OnboardVersion is 0 (default), but device and alias are already configured.
	cfg.Record.Device = "mic"
	cfg.Devices["mic"] = "alsa_input.usb-Blue_Microphones-00.mono-fallback"
	if cfg.NeedsOnboarding() {
		t.Error("expected config with existing device+alias to skip onboarding even with OnboardVersion=0")
	}
}

func TestOnboardVersionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := Default()
	cfg.OnboardVersion = CurrentOnboardVersion

	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if loaded.OnboardVersion != CurrentOnboardVersion {
		t.Errorf("expected OnboardVersion %d after round-trip, got %d",
			CurrentOnboardVersion, loaded.OnboardVersion)
	}
}

func TestLoadFromFileWithDevices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`
[record]
format = "ogg"
device = "mic"

[devices]
mic = "alsa_input.usb-Blue_Microphones-00.mono-fallback"
desktop = "alsa_output.pci-0000_0c_00.4.analog-stereo.monitor"

[device_groups]
zoom = ["mic", "desktop"]
`), 0644)

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Record.Device != "mic" {
		t.Errorf("expected device mic, got %s", cfg.Record.Device)
	}
	if cfg.Devices["mic"] != "alsa_input.usb-Blue_Microphones-00.mono-fallback" {
		t.Errorf("expected mic device, got %s", cfg.Devices["mic"])
	}
	if !reflect.DeepEqual(cfg.DeviceGroups["zoom"], []string{"mic", "desktop"}) {
		t.Errorf("expected zoom group, got %v", cfg.DeviceGroups["zoom"])
	}
}
