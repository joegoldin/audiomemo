package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/transcribe"
)

func localCLIConfig(t *testing.T) (string, string, string) {
	t.Helper()
	t.Setenv("ELEVENLABS_API_KEY", "")
	t.Setenv("ELEVENLABS_API_KEY_FILE", "")
	dir := t.TempDir()
	for name, script := range map[string]string{
		"nemo-speech": `#!/bin/sh
case "$1" in
serve) echo 'no live model in this test' >&2; exit 1 ;;
transcribe) printf '%s\n' '{"text":"Final transcript.","duration":1,"languages":["en"],"words":[{"word":"Final","start":0,"end":0.4},{"word":"transcript.","start":0.4,"end":1}]}' ;;
diarize) printf '%s\n' '{"segments":[{"start":0,"end":1,"speaker":1}]}' ;;
*) exit 2 ;;
esac
`,
		"ffmpeg": `#!/bin/sh
next_output=false
for arg do
 if "$next_output"; then printf 'audio' > "$arg"; next_output=false; fi
 if [ "$arg" = '-y' ]; then next_output=true; fi
 if [ "$arg" = 'pipe:3' ]; then printf 'pcm-data' >&3; fi
done
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := config.Default()
	cfg.OnboardVersion = config.CurrentOnboardVersion
	cfg.Record.OutputDir = dir
	cfg.Transcribe.Nemo.Binary = filepath.Join(dir, "nemo-speech")
	path := filepath.Join(dir, "config.toml")
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	audio := filepath.Join(dir, "existing.wav")
	if err := os.WriteFile(audio, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	return path, audio, dir
}

func TestCLILocalDefaultWithDiarization(t *testing.T) {
	cfg, audio, _ := localCLIConfig(t)
	stdout, stderr, err := run(t, "transcribe", "--config", cfg, "--format", "json", audio)
	if err != nil {
		t.Fatalf("transcribe: %v\n%s", err, stderr)
	}
	var result transcribe.Result
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("JSON: %v\n%s", err, stdout)
	}
	if len(result.Words) != 2 || len(result.SpeakerTurns) != 1 || result.Segments[0].Speaker != "speaker_1" {
		t.Fatalf("result: %+v", result)
	}
	stdout, stderr, err = run(t, "transcribe", "--config", cfg, "--diarize=false", audio)
	if err != nil || strings.Contains(stdout, "speaker_") {
		t.Fatalf("disable diarization: %s %s %v", stdout, stderr, err)
	}
}

func TestCLIRecordRefinesAndPreservesPreview(t *testing.T) {
	cfg, _, dir := localCLIConfig(t)
	stdout, stderr, err := run(t, "record", "--config", cfg, "--no-tui", "--device", "mic", "-t", "--transcribe-args=--diarize", "integration")
	if err != nil {
		t.Fatalf("record: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "speaker_1: Final transcript.") {
		t.Fatalf("missing final transcript: %s\n%s", stdout, stderr)
	}
	files, err := filepath.Glob(filepath.Join(dir, "integration-*-live.txt"))
	if err != nil || len(files) != 1 {
		t.Fatalf("preview files: %v %v", files, err)
	}
	final := strings.TrimSuffix(files[0], "-live.txt") + ".txt"
	data, err := os.ReadFile(final)
	if err != nil || !strings.Contains(string(data), "Final transcript.") {
		t.Fatalf("final file: %s %v", data, err)
	}
}

func TestCLIExplicitLocalFailureDoesNotUpload(t *testing.T) {
	cfg, audio, dir := localCLIConfig(t)
	t.Setenv("ELEVENLABS_API_KEY", "must-not-be-used")
	os.WriteFile(filepath.Join(dir, "nemo-speech"), []byte("#!/bin/sh\nexit 7\n"), 0755)
	_, stderr, err := run(t, "transcribe", "--config", cfg, "--backend", "nemo", audio)
	if err == nil || strings.Contains(stderr, "Falling back") || !strings.Contains(stderr, "exit status 7") {
		t.Fatalf("strict local error: %v %s", err, stderr)
	}
}

func TestCLILocalStreamPreservesNDJSONProtocol(t *testing.T) {
	cfg, _, _ := localCLIConfig(t)
	stdout, stderr, err := run(t, "record", "--config", cfg, "--stream", "--device", "mic", "-t", "--transcribe-args=--backend nemo", "stream")
	if err != nil {
		t.Fatalf("record: %v\n%s", err, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var types []string
	sawFinal := false
	for _, line := range lines {
		var event struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Backend string `json:"backend"`
			Source  string `json:"source"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("non-JSON stdout: %q: %v", line, err)
		}
		types = append(types, event.Type)
		if event.Type == "start" && event.Backend != "nemo" {
			t.Fatalf("start backend: %s", line)
		}
		if event.Type == "final" {
			sawFinal = true
			if event.Backend != "nemo" || event.Source != "batch" || event.Text != "Final transcript." {
				t.Fatalf("final must use local batch text without unsolicited speaker labels: %s", line)
			}
		}
	}
	if len(types) < 3 || types[0] != "start" || types[len(types)-1] != "end" || !sawFinal {
		t.Fatalf("missing/incorrectly ordered events: %v", types)
	}
}
