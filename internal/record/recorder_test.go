package record

import (
	"runtime"
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
