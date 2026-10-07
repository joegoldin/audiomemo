package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joegoldin/audiomemo/internal/config"
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
