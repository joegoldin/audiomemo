package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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
printf '%s\n' "$base" >> "$VM_TEST_SELECTED"
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
		command := exec.Command(testBinary, append([]string{"transcribe", "--config", cfg, "--backend", "whisper"}, args...)...)
		command.Env = append(os.Environ(), "HOME="+home, "PATH="+stubDir, "VM_TEST_SELECTED="+filepath.Join(home, "selected.log"))
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
	for _, filename := range []string{"new.m4a", "notes.m4a", "latest.m4a", "list.m4a"} {
		if err := os.WriteFile(filepath.Join(library, filename), []byte("synthetic audio"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sql = `INSERT INTO ZCLOUDRECORDING VALUES
 ('TEAM MEETING', 'new.m4a', 800000005),
 ('Team meeting notes', 'notes.m4a', 800000010),
 ('Latest', 'latest.m4a', 800000003),
 ('List', 'list.m4a', 800000004),
 ('Not downloaded', 'missing.m4a', 800000099);`
	if out, err := exec.Command("/usr/bin/sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("additional fixtures: %s: %v", out, err)
	}
	for _, tt := range []struct {
		args     []string
		filename string
	}{
		{[]string{"vm", "LaTeSt"}, "notes.m4a"},
		{[]string{"voice-memo", "latest"}, "notes.m4a"},
		{[]string{"vm", "team meeting"}, "new.m4a"},
		{[]string{"vm", "TEAM MEET"}, "notes.m4a"},
		{[]string{"vm", "TMMT"}, "notes.m4a"},
		{[]string{"vm", "--", "latest"}, "latest.m4a"},
		{[]string{"vm", "--", "LIST"}, "list.m4a"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			out, stderr, err := invoke(tt.args...)
			if err != nil || strings.TrimSpace(out) != "Voice memo transcript" {
				t.Fatalf("noninteractive selection: %q %s %v", out, stderr, err)
			}
			data, err := os.ReadFile(filepath.Join(home, "selected.log"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if got := lines[len(lines)-1]; got != tt.filename {
				t.Fatalf("selected %q, want %q", got, tt.filename)
			}
		})
	}
	t.Run("list", func(t *testing.T) {
		before, err := os.ReadFile(filepath.Join(home, "selected.log"))
		if err != nil {
			t.Fatal(err)
		}
		out, stderr, err := invoke("voice-memos", "LiSt")
		if err != nil {
			t.Fatalf("list: %s %v", stderr, err)
		}
		previous := -1
		for _, filename := range []string{"notes.m4a", "new.m4a", "list.m4a", "latest.m4a", "internal-name.m4a"} {
			position := strings.Index(out, filename)
			if position <= previous {
				t.Fatalf("list must show newest first: %s", out)
			}
			previous = position
		}
		if !strings.Contains(out, "Team meeting notes") {
			t.Fatalf("missing display name: %s", out)
		}
		after, err := os.ReadFile(filepath.Join(home, "selected.log"))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("listing ran transcription")
		}
	})
	t.Run("latest output flags", func(t *testing.T) {
		output := filepath.Join(home, "latest-output.json")
		out, stderr, err := invoke("vm", "LATEST", "--quiet", "--format", "json", "--output", output)
		if err != nil || out != "" {
			t.Fatalf("latest flags: %q %s %v", out, stderr, err)
		}
		if data, err := os.ReadFile(output); err != nil || !strings.Contains(string(data), "Voice memo transcript") {
			t.Fatalf("latest explicit output: %q %v", data, err)
		}
		if _, err := os.Stat(filepath.Join(recordings, "notes.json")); !os.IsNotExist(err) {
			t.Fatal("explicit output must replace default destination")
		}
	})
	_, stderr, err = invoke("vm", "absent")
	if err == nil || !strings.Contains(stderr, "no downloaded Voice Memo matches") {
		t.Fatalf("missing name: %s %v", stderr, err)
	}
	for _, filename := range []string{"internal-name.m4a", "new.m4a", "notes.m4a", "latest.m4a", "list.m4a"} {
		path := filepath.Join(library, filename)
		if data, err := os.ReadFile(path); err != nil || string(data) != "synthetic audio" {
			t.Fatalf("library audio modified: %s", filename)
		}
		if _, err := os.Stat(strings.TrimSuffix(path, ".m4a") + ".txt"); !os.IsNotExist(err) {
			t.Fatal("sidecar written into Voice Memos library")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, keyword := range []string{"latest", "list"} {
		_, stderr, err := invoke("vm", keyword)
		if err == nil || !strings.Contains(stderr, "no downloaded Voice Memos found") {
			t.Fatalf("empty library %s: %s %v", keyword, stderr, err)
		}
	}
}

func TestVoiceMemoListPages(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Voice Memos integration")
	}
	home := t.TempDir()
	library := filepath.Join(home, "Library/Group Containers/group.com.apple.VoiceMemos.shared/Recordings")
	if err := os.MkdirAll(library, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 23; i++ {
		path := filepath.Join(library, fmt.Sprintf("memo-%02d.m4a", i))
		if err := os.WriteFile(path, []byte("synthetic audio"), 0600); err != nil {
			t.Fatal(err)
		}
		date := time.Unix(1700000000+int64(i), 0)
		if err := os.Chtimes(path, date, date); err != nil {
			t.Fatal(err)
		}
	}
	invoke := func(args ...string) (string, error) {
		command := exec.Command(testBinary, append([]string{"transcribe", "vm"}, args...)...)
		command.Env = append(os.Environ(), "HOME="+home, "PATH=")
		out, err := command.CombinedOutput()
		return string(out), err
	}
	for _, tt := range []struct {
		args         []string
		first, count int
	}{
		{[]string{"list"}, 22, 10}, {[]string{"LIST", "1"}, 22, 10},
		{[]string{"list", "2"}, 12, 10}, {[]string{"list", "3"}, 2, 3},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			out, err := invoke(tt.args...)
			if err != nil {
				t.Fatalf("list: %s %v", out, err)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			if len(lines) != tt.count+1 {
				t.Fatalf("expected %d results, got %d: %s", tt.count, len(lines)-1, out)
			}
			for i := 0; i < tt.count; i++ {
				if !strings.Contains(lines[i+1], fmt.Sprintf("memo-%02d.m4a", tt.first-i)) {
					t.Fatalf("wrong page/order: %s", out)
				}
			}
		})
	}
	for _, args := range [][]string{{"list", "0"}, {"list", "-1"}, {"list", "nope"}, {"list", "4"}, {"list", "9223372036854775807"}, {"list", "99999999999999999999999"}, {"list", "2", "extra"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := invoke(args...)
			if err == nil {
				t.Fatalf("invalid page accepted: %s", out)
			}
			if !strings.Contains(out, "page") && !strings.Contains(out, "unknown shorthand flag") {
				t.Fatalf("expected page/flag error: %s", out)
			}
		})
	}
	out, err := invoke("--", "list", "2")
	if err == nil || !strings.Contains(out, `no downloaded Voice Memo matches "list 2"`) {
		t.Fatalf("literal list name was interpreted as pagination: %s %v", out, err)
	}
}
