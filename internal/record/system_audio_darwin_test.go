//go:build darwin && cgo

package record

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type nativeCaptureCase struct {
	name    string
	devices []string
}

// Opt in locally: these tests open real audio devices and may prompt for macOS
// audio-recording permission. Normal CI must not capture the host's audio.
func nativeCaptureCases(t *testing.T) []nativeCaptureCase {
	t.Helper()
	if os.Getenv("AUDIOMEMO_TEST_SYSTEM_AUDIO") != "1" {
		t.Skip("set AUDIOMEMO_TEST_SYSTEM_AUDIO=1 to test real Core Audio capture")
	}
	cases := []nativeCaptureCase{{"system", []string{SystemAudioDevice}}}
	if mic := os.Getenv("AUDIOMEMO_TEST_MIC"); mic != "" {
		cases = append(cases, nativeCaptureCase{"mic-and-system", []string{mic, SystemAudioDevice}})
	}
	return cases
}

func TestNativeSystemAudio(t *testing.T) {
	cases := nativeCaptureCases(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "capture.wav")
			rec, err := Start(RecordOpts{Devices: tc.devices, Format: "wav", SampleRate: 48000, Channels: 2, OutputPath: output, LivePCM: true, MaxDuration: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer rec.PCMReader.Close()
			type pcmResult struct {
				bytes int64
				err   error
			}
			pcmDone := make(chan pcmResult, 1)
			go func() {
				n, err := io.Copy(io.Discard, rec.PCMReader)
				pcmDone <- pcmResult{bytes: n, err: err}
			}()
			select {
			case err := <-rec.Done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				rec.Stop()
				t.Fatal("capture did not finish")
			}
			pcm := <-pcmDone
			if pcm.err != nil {
				t.Fatalf("reading live PCM: %v", pcm.err)
			}
			if pcm.bytes < 16000 {
				t.Fatalf("live PCM too short: %d bytes", pcm.bytes)
			}
			info, err := os.Stat(output)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() < 96000 {
				t.Fatalf("recorded WAV too short: %d bytes", info.Size())
			}
		})
	}
}

func TestNativeSystemAudioStop(t *testing.T) {
	for _, tc := range nativeCaptureCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := Start(RecordOpts{Devices: tc.devices, Format: "wav", SampleRate: 48000, Channels: 2, OutputPath: filepath.Join(t.TempDir(), "stopped.wav")})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				rec.Stop()
				if err := rec.Wait(); err != nil {
					t.Errorf("finalizing native recording: %v", err)
				}
			}()
			select {
			case <-rec.Level:
			case <-time.After(5 * time.Second):
				t.Fatal("capture did not produce levels")
			}
			rec.Stop()
			select {
			case err := <-rec.Done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				if err := rec.cmd.Process.Kill(); err != nil {
					t.Errorf("killing stalled encoder: %v", err)
				}
				err := rec.Wait()
				t.Fatalf("stopping native capture did not finalize recording: %v\n%s", err, rec.StderrTail())
			}
		})
	}
}

func TestNativeSystemAudioEncoderStartFailure(t *testing.T) {
	nativeCaptureCases(t)
	t.Setenv("PATH", t.TempDir())
	rec, err := Start(RecordOpts{Device: SystemAudioDevice, Format: "wav", SampleRate: 48000, Channels: 2, OutputPath: filepath.Join(t.TempDir(), "failed.wav")})
	if rec != nil {
		rec.Stop()
		if waitErr := rec.Wait(); waitErr != nil {
			t.Errorf("unexpected recorder cleanup: %v", waitErr)
		}
		t.Fatal("missing ffmpeg must not return a recorder")
	}
	if err == nil {
		t.Fatal("expected missing ffmpeg to fail after creating the tap")
	}
}
