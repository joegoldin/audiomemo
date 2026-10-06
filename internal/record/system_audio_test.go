package record

import (
	"strings"
	"testing"
)

func TestSystemAudioInputArgs(t *testing.T) {
	for _, live := range []bool{false, true} {
		opts := RecordOpts{Device: "system-audio", Format: "wav", SampleRate: 48000, Channels: 2, OutputPath: "out.wav", LivePCM: live}
		args := strings.Join(BuildFFmpegArgs(opts), " ")
		if !strings.Contains(args, "-f f32le -ar 48000 -ac 2 -i pipe:3") {
			t.Fatalf("system audio must read native PCM, not open an AVFoundation device: %s", args)
		}
	}
}

func TestSystemAudioUsesNativeSampleRate(t *testing.T) {
	opts := RecordOpts{Device: SystemAudioDevice, tapSampleRate: 44100, tapChannels: 2}
	args := strings.Join(BuildFFmpegArgs(opts), " ")
	if !strings.Contains(args, "-f f32le -ar 44100 -ac 2 -i pipe:3") {
		t.Fatalf("tap samples must be interpreted at their native rate: %s", args)
	}
}

func TestSystemAudioUnavailable(t *testing.T) {
	if systemAudioAvailable() {
		t.Skip("native system audio is available in this build")
	}
	rec, err := Start(RecordOpts{Device: SystemAudioDevice})
	if rec != nil {
		rec.Stop()
		if waitErr := rec.Wait(); waitErr != nil {
			t.Errorf("unexpected recorder cleanup: %v", waitErr)
		}
		t.Fatal("unsupported capture must not return a recorder")
	}
	if err == nil || !strings.Contains(err.Error(), "macOS 14.2") {
		t.Fatalf("unsupported builds must return an actionable error, got %v", err)
	}
}

func TestSystemAudioDuplicateInput(t *testing.T) {
	rec, err := Start(RecordOpts{Devices: []string{SystemAudioDevice, SystemAudioDevice}})
	if rec != nil {
		rec.Stop()
		if waitErr := rec.Wait(); waitErr != nil {
			t.Errorf("unexpected recorder cleanup: %v", waitErr)
		}
		t.Fatal("duplicate inputs must not return a recorder")
	}
	if err == nil || !strings.Contains(err.Error(), "only be selected once") {
		t.Fatalf("duplicate pipe readers must be rejected, got %v", err)
	}
}

func TestSystemAudioMixedWithMicrophone(t *testing.T) {
	opts := RecordOpts{Devices: []string{"mic", "system-audio"}, Format: "wav", SampleRate: 48000, Channels: 2, OutputPath: "out.wav", LivePCM: true}
	args, err := BuildFFmpegArgsMulti(opts)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f f32le -ar 48000 -ac 2 -i pipe:3") || !strings.Contains(joined, "asplit=2[a][b]") {
		t.Fatalf("mic and system audio must reach recording and live transcription: %s", joined)
	}
	if !strings.Contains(joined, "aresample=async=1:first_pts=0") {
		t.Fatalf("independent input clocks must be aligned before mixing: %s", joined)
	}
}
