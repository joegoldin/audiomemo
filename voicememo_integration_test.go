package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestVoiceMemoTranscription(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Voice Memos integration")
	}
	home := t.TempDir()
	library := filepath.Join(home, "Library/Group Containers/group.com.apple.VoiceMemos.shared/Recordings")
	if err := os.MkdirAll(library, 0700); err != nil {
		t.Fatal(err)
	}
	audio := filepath.Join(library, "internal-name.m4a")
	if err := os.WriteFile(audio, []byte("synthetic audio"), 0600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(library, "CloudRecordings.db")
	sql := `CREATE TABLE ZCLOUDRECORDING (ZCUSTOMLABEL TEXT, ZPATH TEXT, ZDATE REAL);
 INSERT INTO ZCLOUDRECORDING VALUES ('Team meeting', 'internal-name.m4a', 800000000);`
	if out, err := exec.Command("/usr/bin/sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s: %v", out, err)
	}
	stubDir := t.TempDir()
	script := `#!/bin/sh
set -eu
out=''
while [ "$#" -gt 0 ]; do
 case "$1" in
  --output_dir) out="$2"; shift 2 ;;
  *) audio="$1"; shift ;;
 esac
done
base="${audio##*/}"
printf '%s' '{"text":"Voice memo transcript","segments":[{"start":0,"end":1,"text":"Voice memo transcript"}]}' > "$out/${base%.*}.json"
`
	if err := os.WriteFile(filepath.Join(stubDir, "whisper"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	recordings := filepath.Join(home, "custom-recordings")
	cfg := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfg, []byte("[record]\noutput_dir = "+strconv.Quote(recordings)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(args ...string) (string, string, error) {
		t.Helper()
		command := exec.Command(testBinary, append([]string{"transcribe"}, append(args, "--config", cfg, "--backend", "whisper")...)...)
		command.Env = append(os.Environ(), "HOME="+home, "PATH="+stubDir)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		return stdout.String(), stderr.String(), err
	}
	for _, args := range [][]string{{"vm", "TEAM MEETING"}, {"voice-memo", "internal-name.m4a"}, {"vm", "Team", "meeting"}} {
		out, stderr, err := invoke(args...)
		if err != nil {
			t.Fatalf("%v: %s: %v", args, stderr, err)
		}
		if strings.TrimSpace(out) != "Voice memo transcript" {
			t.Fatalf("unexpected stdout: %q", out)
		}
	}
	saved, err := os.ReadFile(filepath.Join(recordings, "internal-name.txt"))
	if err != nil || string(saved) != "Voice memo transcript" {
		t.Fatalf("default destination: %q %v", saved, err)
	}
	explicit := filepath.Join(home, "chosen.json")
	out, stderr, err := invoke("vm", "meeting", "--output", explicit, "--format", "json", "--quiet")
	if err != nil || out != "" {
		t.Fatalf("explicit output: %q %s %v", out, stderr, err)
	}
	if data, err := os.ReadFile(explicit); err != nil || !strings.Contains(string(data), "Voice memo transcript") {
		t.Fatalf("explicit file: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(recordings, "internal-name.json")); !os.IsNotExist(err) {
		t.Fatal("--output should replace default destination")
	}
	if _, err := os.Stat(filepath.Join(library, "internal-name.txt")); !os.IsNotExist(err) {
		t.Fatal("sidecar written in library")
	}
	original, err := os.ReadFile(audio)
	if err != nil || string(original) != "synthetic audio" {
		t.Fatal("original audio changed")
	}
	_, stderr, err = invoke("vm", "absent")
	if err == nil || !strings.Contains(stderr, "no downloaded Voice Memo matches") {
		t.Fatalf("missing name: %s %v", stderr, err)
	}
}
