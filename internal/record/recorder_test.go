package record

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestBuildFFmpegArgs(t *testing.T) {
	opts := RecordOpts{
		Device:     "default",
		Format:     "ogg",
		SampleRate: 48000,
		Channels:   1,
		OutputPath: "/tmp/test.ogg",
	}
	args := BuildFFmpegArgs(opts)

	// Should contain the output path as last arg
	if args[len(args)-1] != "/tmp/test.ogg" {
		t.Errorf("expected output path as last arg, got %s", args[len(args)-1])
	}

	// Should contain codec
	found := false
	for _, a := range args {
		if a == "libopus" {
			found = true
		}
	}
	if opts.Format == "ogg" && !found {
		t.Error("expected libopus codec for ogg format")
	}
}

func TestBuildFFmpegArgsWav(t *testing.T) {
	opts := RecordOpts{
		Device:     "default",
		Format:     "wav",
		SampleRate: 44100,
		Channels:   2,
		OutputPath: "/tmp/test.wav",
	}
	args := BuildFFmpegArgs(opts)
	found := false
	for _, a := range args {
		if a == "pcm_s16le" {
			found = true
		}
	}
	if !found {
		t.Error("expected pcm_s16le codec for wav format")
	}
}

func TestInputFormatForPlatform(t *testing.T) {
	f := InputFormat()
	switch runtime.GOOS {
	case "linux":
		if f != "pulse" {
			t.Errorf("expected pulse on linux, got %s", f)
		}
	case "darwin":
		if f != "avfoundation" {
			t.Errorf("expected avfoundation on darwin, got %s", f)
		}
	}
}

func TestCodecForFormat(t *testing.T) {
	tests := []struct {
		format string
		codec  string
	}{
		{"ogg", "libopus"},
		{"wav", "pcm_s16le"},
		{"flac", "flac"},
		{"mp3", "libmp3lame"},
	}
	for _, tt := range tests {
		c := CodecForFormat(tt.format)
		if c != tt.codec {
			t.Errorf("format %s: expected %s, got %s", tt.format, tt.codec, c)
		}
	}
}

func TestGenerateFilename(t *testing.T) {
	name := GenerateFilename("ogg", "meeting")
	if name == "" {
		t.Error("expected non-empty filename")
	}
	if len(name) < 10 {
		t.Error("expected filename with timestamp")
	}
}

// containsArg returns true if args contains the given value.
func containsArg(args []string, val string) bool {
	for _, a := range args {
		if a == val {
			return true
		}
	}
	return false
}

// argAfter returns the argument immediately following key, or "" if not found.
func argAfter(args []string, key string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestBuildFFmpegArgsMultiSingleDevice(t *testing.T) {
	opts := RecordOpts{
		Devices:    []string{"default"},
		Format:     "ogg",
		SampleRate: 48000,
		Channels:   1,
		OutputPath: "/tmp/test.ogg",
	}
	args, err := BuildFFmpegArgsMulti(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Single device should fall back to BuildFFmpegArgs (no filter_complex).
	if containsArg(args, "-filter_complex") {
		t.Error("single device should not use -filter_complex")
	}
	if !containsArg(args, "-af") {
		t.Error("single device should use -af for VU meter filters")
	}
	if args[len(args)-1] != "/tmp/test.ogg" {
		t.Errorf("expected output path as last arg, got %s", args[len(args)-1])
	}
}

func TestBuildFFmpegArgsMultiTwoDevices(t *testing.T) {
	opts := RecordOpts{
		Devices:    []string{"alsa_input.mic", "alsa_output.monitor"},
		Format:     "ogg",
		SampleRate: 48000,
		Channels:   1,
		OutputPath: "/tmp/test.ogg",
	}
	args, err := BuildFFmpegArgsMulti(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inputFmt := InputFormat()

	// Should have two -f/-i pairs.
	inputCount := 0
	for i, a := range args {
		if a == "-f" && i+1 < len(args) && args[i+1] == inputFmt {
			inputCount++
		}
	}
	if inputCount != 2 {
		t.Errorf("expected 2 input format flags, got %d", inputCount)
	}

	// Should have -filter_complex with amix=inputs=2.
	fc := argAfter(args, "-filter_complex")
	if fc == "" {
		t.Fatal("expected -filter_complex argument")
	}
	if !strings.Contains(fc, "amix=inputs=2") {
		t.Errorf("filter_complex should contain amix=inputs=2, got: %s", fc)
	}
	if !strings.Contains(fc, "[0:a][1:a]") {
		t.Errorf("filter_complex should reference [0:a][1:a], got: %s", fc)
	}
	if !strings.Contains(fc, "astats") {
		t.Errorf("filter_complex should contain astats VU filter, got: %s", fc)
	}
	if !strings.Contains(fc, "ametadata") {
		t.Errorf("filter_complex should contain ametadata, got: %s", fc)
	}

	// Should have -map "[a]".
	mapArg := argAfter(args, "-map")
	if mapArg != "[a]" {
		t.Errorf("expected -map [a], got: %s", mapArg)
	}

	// Should NOT have standalone -af (filters are in filter_complex).
	if containsArg(args, "-af") {
		t.Error("multi-device should not use standalone -af")
	}

	// Output should be last arg.
	if args[len(args)-1] != "/tmp/test.ogg" {
		t.Errorf("expected output path as last arg, got %s", args[len(args)-1])
	}

	// Should contain codec and bitrate.
	if !containsArg(args, "libopus") {
		t.Error("expected libopus codec for ogg format")
	}
}

func TestBuildFFmpegArgsMultiThreeDevices(t *testing.T) {
	opts := RecordOpts{
		Devices:    []string{"mic1", "mic2", "desktop_monitor"},
		Format:     "flac",
		SampleRate: 44100,
		Channels:   1,
		OutputPath: "/tmp/test.flac",
	}
	args, err := BuildFFmpegArgsMulti(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inputFmt := InputFormat()

	// Should have three -f/-i pairs.
	inputCount := 0
	for i, a := range args {
		if a == "-f" && i+1 < len(args) && args[i+1] == inputFmt {
			inputCount++
		}
	}
	if inputCount != 3 {
		t.Errorf("expected 3 input format flags, got %d", inputCount)
	}

	// filter_complex should reference all three inputs and amix=inputs=3.
	fc := argAfter(args, "-filter_complex")
	if fc == "" {
		t.Fatal("expected -filter_complex argument")
	}
	if !strings.Contains(fc, "amix=inputs=3") {
		t.Errorf("filter_complex should contain amix=inputs=3, got: %s", fc)
	}
	if !strings.Contains(fc, "[0:a][1:a][2:a]") {
		t.Errorf("filter_complex should reference [0:a][1:a][2:a], got: %s", fc)
	}

	// Codec should be flac (no bitrate flag).
	if !containsArg(args, "flac") {
		t.Error("expected flac codec for flac format")
	}
	// flac should NOT have -b:a.
	if containsArg(args, "-b:a") {
		t.Error("flac format should not have -b:a bitrate flag")
	}

	if args[len(args)-1] != "/tmp/test.flac" {
		t.Errorf("expected output path as last arg, got %s", args[len(args)-1])
	}
}

func TestBuildFFmpegArgsMultiEmptyDevices(t *testing.T) {
	opts := RecordOpts{
		Devices:    []string{},
		Format:     "ogg",
		SampleRate: 48000,
		Channels:   1,
		OutputPath: "/tmp/test.ogg",
	}
	_, err := BuildFFmpegArgsMulti(opts)
	if err == nil {
		t.Fatal("expected error for empty devices list")
	}
}

// Ensure the test helpers compile (use fmt and strings).
var _ = fmt.Sprintf
var _ = strings.Contains
