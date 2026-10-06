package record

import (
	"os"
	"strconv"
)

// SystemAudioDevice is a synthetic source backed by a macOS Core Audio tap.
const SystemAudioDevice = "system-audio"

type systemAudioCapture struct {
	reader     *os.File
	sampleRate int
	channels   int
	failed     <-chan struct{}
	close      func() error
}

func usesSystemAudio(opts RecordOpts) bool {
	if len(opts.Devices) == 0 {
		return opts.Device == SystemAudioDevice
	}
	for _, device := range opts.Devices {
		if device == SystemAudioDevice {
			return true
		}
	}
	return false
}

func ffmpegInputArgs(device string, opts RecordOpts) []string {
	if device == SystemAudioDevice {
		rate, channels := opts.tapSampleRate, opts.tapChannels
		if rate == 0 {
			rate, channels = 48000, 2
		}
		args := []string{"-f", "f32le", "-ar", strconv.Itoa(rate), "-ac", strconv.Itoa(channels)}
		args = append(args, ffmpegDurationArgs(opts.MaxDuration)...)
		return append(args, "-i", "pipe:3")
	}
	if device == "" {
		device = "default"
	}
	if InputFormat() == "avfoundation" && device[0] != ':' {
		device = ":" + device
	}
	args := []string{"-f", InputFormat()}
	args = append(args, ffmpegDurationArgs(opts.MaxDuration)...)
	return append(args, "-i", device)
}
