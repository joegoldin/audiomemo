package record

import (
	"fmt"
	"math"
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

func TestGenerateFilenameWithLabel(t *testing.T) {
	name := GenerateFilename("ogg", "my_cool_meeting")
	if !strings.HasPrefix(name, "my_cool_meeting-") {
		t.Errorf("expected prefix my_cool_meeting-, got %s", name)
	}
	if !strings.HasSuffix(name, ".ogg") {
		t.Errorf("expected .ogg suffix, got %s", name)
	}
}

func TestGenerateFilenameWithoutLabel(t *testing.T) {
	name := GenerateFilename("ogg", "")
	if !strings.HasPrefix(name, "recording-") {
		t.Errorf("expected prefix recording-, got %s", name)
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

func TestGenerateClipFilename(t *testing.T) {
	name := GenerateClipFilename("ogg", "interview", 3)
	if !strings.HasPrefix(name, "interview-003-") {
		t.Errorf("expected prefix interview-003-, got %s", name)
	}
	if !strings.HasSuffix(name, ".ogg") {
		t.Errorf("expected .ogg suffix, got %s", name)
	}
}

func TestGenerateClipFilenameFirstClip(t *testing.T) {
	name := GenerateClipFilename("wav", "session", 1)
	if !strings.HasPrefix(name, "session-001-") {
		t.Errorf("expected prefix session-001-, got %s", name)
	}
	if !strings.HasSuffix(name, ".wav") {
		t.Errorf("expected .wav suffix, got %s", name)
	}
}

func TestAppendPCMPipeArgs(t *testing.T) {
	base := []string{"-f", "pulse", "-i", "default"}
	result := appendPCMPipeArgs(base, 3)

	// Original args should be unchanged (result is a new slice).
	if len(base) != 4 {
		t.Errorf("base slice should not be modified, got len %d", len(base))
	}

	// Appended args: -f s16le -ar 16000 -ac 1 pipe:3
	expected := []string{"-f", "pulse", "-i", "default", "-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:3"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(result), result)
	}
	for i, v := range expected {
		if result[i] != v {
			t.Errorf("arg[%d]: expected %q, got %q", i, v, result[i])
		}
	}

	// Also verify with a different fd number.
	result5 := appendPCMPipeArgs([]string{}, 5)
	if !containsArg(result5, "pipe:5") {
		t.Errorf("expected pipe:5 in args, got %v", result5)
	}
	if argAfter(result5, "-f") != "s16le" {
		t.Errorf("expected -f s16le, got -f %s", argAfter(result5, "-f"))
	}
	if argAfter(result5, "-ar") != "16000" {
		t.Errorf("expected -ar 16000, got -ar %s", argAfter(result5, "-ar"))
	}
	if argAfter(result5, "-ac") != "1" {
		t.Errorf("expected -ac 1, got -ac %s", argAfter(result5, "-ac"))
	}
}

func TestStderrTapCapturesRMSAndTail(t *testing.T) {
	r := &Recorder{Level: make(chan float64, 4)}
	tap := &stderrTap{r: r}

	// Mix of RMS lines (consumed into Level) and arbitrary lines (kept in tail).
	chunks := []string{
		"[avfoundation] some startup banner\n",
		"frame=  10 ",
		"fps=30 size=N/A\n",
		"[Parsed_astats_1 @ 0x123] lavfi.astats.Overall.RMS_level=-12.5\n",
		"[Parsed_astats_1 @ 0x123] lavfi.astats.Overall.RMS_level=-inf\n",
		"Audio device not found\n",
	}
	for _, c := range chunks {
		if _, err := tap.Write([]byte(c)); err != nil {
			t.Fatalf("unexpected write error: %v", err)
		}
	}

	close(r.Level)
	var levels []float64
	for v := range r.Level {
		levels = append(levels, v)
	}
	if len(levels) != 2 {
		t.Fatalf("expected 2 RMS levels, got %d: %v", len(levels), levels)
	}
	if levels[0] != -12.5 {
		t.Errorf("expected first RMS -12.5, got %v", levels[0])
	}
	if !math.IsInf(levels[1], -1) {
		t.Errorf("expected second RMS -Inf, got %v", levels[1])
	}

	tail := r.StderrTail()
	if !strings.Contains(tail, "Audio device not found") {
		t.Errorf("tail missing error line:\n%s", tail)
	}
	if strings.Contains(tail, "RMS_level=") {
		t.Errorf("tail should not contain RMS lines:\n%s", tail)
	}
	// Split-across-Write line should reassemble correctly.
	if !strings.Contains(tail, "frame=  10 fps=30 size=N/A") {
		t.Errorf("split line not reassembled:\n%s", tail)
	}
}

func TestStderrTailRingBufferTrims(t *testing.T) {
	r := &Recorder{Level: make(chan float64, 1)}
	tap := &stderrTap{r: r}

	for i := 0; i < maxStderrTailLines+10; i++ {
		fmt.Fprintf(&writeTo{tap}, "line-%03d\n", i)
	}

	lines := strings.Split(r.StderrTail(), "\n")
	if len(lines) != maxStderrTailLines {
		t.Fatalf("expected exactly %d lines, got %d", maxStderrTailLines, len(lines))
	}
	// Oldest should have been dropped; the last line is line-039.
	last := fmt.Sprintf("line-%03d", maxStderrTailLines+10-1)
	if lines[len(lines)-1] != last {
		t.Errorf("expected last %q, got %q", last, lines[len(lines)-1])
	}
	if lines[0] == "line-000" {
		t.Errorf("oldest line should have been trimmed, got %q", lines[0])
	}
}

// writeTo lets fmt.Fprintf target a stderrTap without exposing its Write
// method directly through an interface variable in the test body.
type writeTo struct{ w *stderrTap }

func (w *writeTo) Write(p []byte) (int, error) { return w.w.Write(p) }

// Ensure the test helpers compile (use fmt and strings).
var _ = fmt.Sprintf
var _ = strings.Contains
