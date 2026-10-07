package cmd

import (
	"bytes"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/voicememo"
)

func TestVoiceMemoTranscriptPath(t *testing.T) {
	oldConfig, oldOutput, oldFormat := tConfig, tOutput, tFormat
	t.Cleanup(func() { tConfig, tOutput, tFormat = oldConfig, oldOutput, oldFormat })
	cfg := config.Default()
	cfg.Record.OutputDir = filepath.Join(t.TempDir(), "recordings")
	tConfig = filepath.Join(t.TempDir(), "config.toml")
	if err := cfg.SaveTo(tConfig); err != nil {
		t.Fatal(err)
	}
	audio := filepath.Join(t.TempDir(), "memo.m4a")
	tOutput, tFormat = "", "srt"
	got, err := voiceMemoTranscriptPath(audio)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cfg.Record.OutputDir, "memo.srt"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if info, err := os.Stat(cfg.Record.OutputDir); err != nil || !info.IsDir() {
		t.Fatalf("output directory not created: %v", err)
	}
	tOutput = filepath.Join(t.TempDir(), "explicit.txt")
	got, err = voiceMemoTranscriptPath(audio)
	if err != nil || got != "" {
		t.Fatalf("explicit output should skip autosave: %q %v", got, err)
	}
	tOutput = filepath.Join(filepath.Dir(audio), "memo.txt")
	if _, err := voiceMemoTranscriptPath(audio); err == nil {
		t.Fatal("must not write to library")
	}
}

func TestVoiceMemoAliases(t *testing.T) {
	for _, alias := range []string{"vm", "voice-memo", "voice-memos"} {
		cmd, args, err := rootCmd.Find([]string{"transcribe", alias, "Team meeting"})
		if err != nil || cmd != transcribeVoiceMemoCmd || len(args) != 1 {
			t.Fatalf("alias %s: %v %v %v", alias, cmd, args, err)
		}
	}
}

func TestVoiceMemoOutputProtectsLibrary(t *testing.T) {
	oldOutput := tOutput
	t.Cleanup(func() { tOutput = oldOutput })
	library := t.TempDir()
	audio := filepath.Join(library, "memo.m4a")
	outside := t.TempDir()
	alias := filepath.Join(outside, "alias")
	if err := os.Symlink(library, alias); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(outside, "dangling.txt")
	if err := os.Symlink(filepath.Join(library, "new.txt"), dangling); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(library, "subdir", "memo.txt"), filepath.Join(alias, "memo.txt"), dangling} {
		tOutput = path
		if _, err := voiceMemoTranscriptPath(audio); err == nil {
			t.Errorf("allowed library write through %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(library, "subdir")); !os.IsNotExist(err) {
		t.Fatal("created directory inside library")
	}
}

func TestVoiceMemoList(t *testing.T) {
	duration := 709.55
	recordings := []voicememo.Recording{{Name: "Pretty meeting title", Path: "/Library/Voice Memos/a #1?.m4a", Date: time.Unix(0, 0), Duration: &duration}}
	for _, hyperlinks := range []bool{false, true} {
		var out bytes.Buffer
		if err := writeVoiceMemoList(&out, recordings, hyperlinks); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		for _, want := range []string{"DATE", "NAME", "DURATION", "FILE", "Pretty meeting title", "11:50", "file:///Library/Voice%20Memos/a%20%231%3F.m4a"} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q: %s", want, text)
			}
		}
		if strings.Contains(text, "\x1b]8;") != hyperlinks {
			t.Errorf("terminal links=%v: %q", hyperlinks, text)
		}
		if strings.Contains(text, `"Pretty meeting title"`) {
			t.Fatal("display title should not be quoted")
		}
	}
	recordings[0].Name = "Unsafe\nname\t\x1b[31m"
	var out bytes.Buffer
	if err := writeVoiceMemoList(&out, recordings, false); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "\n") != 2 || strings.Contains(out.String(), "\x1b") {
		t.Fatalf("unescaped control characters: %q", out.String())
	}
	reader, writer := io.Pipe()
	reader.Close()
	defer writer.Close()
	if err := writeVoiceMemoList(writer, recordings, false); err == nil {
		t.Fatal("must report output failure")
	}
}

func TestVoiceMemoDuration(t *testing.T) {
	if got := voiceMemoDuration(nil); got != "—" {
		t.Fatalf("missing duration: %s", got)
	}
	for _, tt := range []struct {
		seconds float64
		want    string
	}{
		{0, "0:00"}, {0.512, "0:01"}, {59.6, "1:00"}, {709.55, "11:50"}, {9132.2, "2:32:12"},
		{-1, "—"}, {math.NaN(), "—"}, {math.Inf(1), "—"},
	} {
		if got := voiceMemoDuration(&tt.seconds); got != tt.want {
			t.Errorf("duration %v = %s, want %s", tt.seconds, got, tt.want)
		}
	}
}
