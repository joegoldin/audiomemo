package transcribe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
)

func fakeNemo(t *testing.T) (config.NemoConfig, string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	t.Setenv("NEMO_TEST_LOG", log)
	t.Setenv("NEMO_TEST_ASR", `{"text":"Hello there. Welcome back!","duration":4,"languages":["en"],"words":[{"word":"Hello","start":0,"end":0.4},{"word":"there.","start":0.4,"end":1},{"word":"Welcome","start":2,"end":2.4},{"word":"back!","start":2.4,"end":3}]}`)
	t.Setenv("NEMO_TEST_DIAR", `{"segments":[{"start":0,"end":1,"speaker":1},{"start":2,"end":3,"speaker":2}]}`)
	for name, script := range map[string]string{
		"nemo-speech": `#!/bin/sh
printf '%s\n' "$@" >> "$NEMO_TEST_LOG"
case "$1" in
 transcribe) printf '%s\n' "$NEMO_TEST_ASR" ;;
 diarize) printf '%s\n' "$NEMO_TEST_DIAR" ;;
 *) exit 9 ;;
esac
`,
		"ffmpeg": `#!/bin/sh
printf '%s\n' "convert" "$@" >> "$NEMO_TEST_LOG"
for last do :; done
printf 'RIFF test PCM' > "$last"
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	audio := filepath.Join(dir, "recording with spaces.wav")
	if err := os.WriteFile(audio, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Transcribe.Nemo
	cfg.Binary = filepath.Join(dir, "nemo-speech")
	return cfg, audio, log
}

func TestNemoSequentialASRAndDiarization(t *testing.T) {
	cfg, audio, log := fakeNemo(t)
	result, err := NewNemo(cfg).Transcribe(t.Context(), audio, TranscribeOpts{Diarize: true, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Language != "en" || result.Duration != 4 || len(result.Words) != 4 || len(result.SpeakerTurns) != 2 {
		t.Fatalf("result: %+v", result)
	}
	want := []Segment{{Start: 0, End: 1, Text: "Hello there.", Speaker: "speaker_1"}, {Start: 2, End: 3, Text: "Welcome back!", Speaker: "speaker_2"}}
	if !reflect.DeepEqual(result.Segments, want) {
		t.Fatalf("segments: %+v", result.Segments)
	}
	for _, format := range []OutputFormat{FormatText, FormatJSON, FormatSRT, FormatVTT} {
		if !strings.Contains(result.Format(format), "speaker_2") {
			t.Errorf("missing speaker in %s", format)
		}
	}
	args, _ := os.ReadFile(log)
	text := string(args)
	for _, expected := range []string{"convert\n", "-c:a\npcm_s16le", "--model\nparakeet-tdt", "--language\nen", "--preset\nv3-offline", "--model\nnemotron-3-diarization"} {
		if !strings.Contains(text, expected) {
			t.Errorf("missing %q in %s", expected, text)
		}
	}
	if strings.Index(text, "transcribe\n") > strings.Index(text, "diarize\n") {
		t.Fatal("diarization must follow ASR")
	}
	if strings.Contains(text, "--offline\n") {
		t.Fatal("must not use length-limited full-attention diarization")
	}
}

func TestNemoNoDiarizationAndModelOverride(t *testing.T) {
	cfg, audio, log := fakeNemo(t)
	result, err := NewNemo(cfg).Transcribe(t.Context(), audio, TranscribeOpts{Model: "/models/custom.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(log)
	if strings.Contains(string(args), "diarize\n") || !strings.Contains(string(args), "/models/custom.gguf") {
		t.Fatalf("args: %s", args)
	}
	if len(result.SpeakerTurns) != 0 || result.Segments[0].Speaker != "" {
		t.Fatal("unexpected diarization")
	}
}

func TestNemoRejectsMalformedOutput(t *testing.T) {
	for _, output := range []string{`no JSON`, `{}`, `{"text":"hello"}`, `{"text":"hello","words":[{"word":"hello","start":3,"end":1}]}`} {
		t.Run(output, func(t *testing.T) {
			cfg, audio, _ := fakeNemo(t)
			t.Setenv("NEMO_TEST_ASR", output)
			if _, err := NewNemo(cfg).Transcribe(t.Context(), audio, TranscribeOpts{}); err == nil {
				t.Fatal("expected output error")
			}
		})
	}
}

func TestNemoEmptyAudioAndDiarizationFailure(t *testing.T) {
	cfg, audio, log := fakeNemo(t)
	t.Setenv("NEMO_TEST_DIAR", `{}`)
	if _, err := NewNemo(cfg).Transcribe(t.Context(), audio, TranscribeOpts{Diarize: true}); err == nil {
		t.Fatal("missing diarization must fail")
	}
	t.Setenv("NEMO_TEST_ASR", `{"text":"","words":[],"duration":3}`)
	os.WriteFile(log, nil, 0600)
	result, err := NewNemo(cfg).Transcribe(t.Context(), audio, TranscribeOpts{Diarize: true})
	if err != nil || result.Text != "" {
		t.Fatalf("silence: %v %v", result, err)
	}
	args, _ := os.ReadFile(log)
	if strings.Contains(string(args), "diarize\n") {
		t.Fatal("silence should not require speaker turns")
	}
}

func TestAssignSpeakersOverlapAndUnassignedWords(t *testing.T) {
	r := &Result{Words: []Word{{Word: "one", Start: 0, End: 1}, {Word: "two", Start: 1, End: 2}, {Word: "?", Start: 2, End: 2}, {Word: "unknown", Start: 4, End: 5}}, SpeakerTurns: []SpeakerTurn{{Start: 0, End: 1.1, Speaker: "a"}, {Start: 0.9, End: 2, Speaker: "b"}}}
	assignSpeakers(r)
	for i, want := range []string{"a", "b", "b", ""} {
		if r.Words[i].Speaker != want {
			t.Errorf("word %d: %s != %s", i, r.Words[i].Speaker, want)
		}
	}
	if len(r.SpeakerTurns) != 2 {
		t.Fatal("lost overlapping turns")
	}
}

func TestRunNemoFailureAndCancellation(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "nemo")
	os.WriteFile(binary, []byte("#!/bin/sh\necho model-failed >&2\nexit 2\n"), 0755)
	if _, err := runNemo(t.Context(), binary, []string{"transcribe"}, false); err == nil || !strings.Contains(err.Error(), "model-failed") {
		t.Fatalf("error: %v", err)
	}
	os.WriteFile(binary, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0755)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := runNemo(ctx, binary, []string{"transcribe"}, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel: %v", err)
	}
}
