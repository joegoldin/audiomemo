package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestExplicitTranscribeOutputPreservesSidecars(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{
		"nemo-speech": `#!/bin/sh
case "$1" in
 transcribe) printf '%s' '{"text":"New transcript.","duration":1,"words":[{"word":"New transcript.","start":0,"end":1}]}' ;;
 diarize) printf '%s' '{"segments":[{"start":0,"end":1,"speaker":1}]}' ;;
 *) exit 1 ;;
esac
`,
		"ffmpeg": `#!/bin/sh
for last do :; done
printf 'RIFF synthetic audio' > "$last"
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[transcribe.nemo]\nbinary = "+strconv.Quote(filepath.Join(dir, "nemo-speech"))+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"text", "json", "srt", "vtt"} {
		t.Run(format, func(t *testing.T) {
			folder := t.TempDir()
			audio := filepath.Join(folder, "recording.ogg")
			if err := os.WriteFile(audio, []byte("original audio"), 0600); err != nil {
				t.Fatal(err)
			}
			ext := format
			if format == "text" {
				ext = "txt"
			}
			sidecar := filepath.Join(folder, "recording."+ext)
			explicit := filepath.Join(folder, "comparison."+ext)
			invoke := func(output string) error {
				t.Helper()
				command := exec.Command(testBinary, "transcribe", audio, "--config", configPath, "--backend", "nemo", "--format", format, "--quiet", "--output", output)
				command.Env = append(os.Environ(), "PATH="+dir)
				out, err := command.CombinedOutput()
				if err != nil {
					t.Logf("transcribe: %s", out)
				}
				return err
			}
			// Explicit output must not create a second file beside the audio.
			if err := invoke(explicit); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
				t.Fatal("--output unexpectedly created an automatic sidecar")
			}
			if data, err := os.ReadFile(explicit); err != nil || len(data) == 0 {
				t.Fatalf("missing explicit output: %v", err)
			}
			// It must not overwrite an existing original transcript, even when
			// writing the explicit destination fails.
			if err := os.WriteFile(sidecar, []byte("original transcript"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := invoke(explicit); err != nil {
				t.Fatal(err)
			}
			if err := invoke(filepath.Join(folder, "absent", "output."+ext)); err == nil {
				t.Fatal("expected explicit destination write error")
			}
			if data, err := os.ReadFile(sidecar); err != nil || string(data) != "original transcript" {
				t.Fatal("--output overwrote the original transcript")
			}
			if data, err := os.ReadFile(audio); err != nil || string(data) != "original audio" {
				t.Fatal("original audio changed")
			}
		})
	}
}
