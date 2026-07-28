package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromoteLiveTranscript(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "memo.ogg")
	live := filepath.Join(dir, "memo-live.txt")
	if err := os.WriteFile(live, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	promoted, err := promoteLiveTranscript(audio)
	if err != nil {
		t.Fatalf("promote failed: %v", err)
	}
	want := filepath.Join(dir, "memo.txt")
	if promoted != want {
		t.Errorf("expected promoted path %q, got %q", want, promoted)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("canonical transcript not written: %v", err)
	}
	if string(data) != "hello world\n" {
		t.Errorf("unexpected content: %q", data)
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("live file should be preserved: %v", err)
	}
}

func TestPromoteLiveTranscriptMissing(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "memo.ogg")

	promoted, err := promoteLiveTranscript(audio)
	if err != nil {
		t.Fatalf("expected nil error for missing live file, got %v", err)
	}
	if promoted != "" {
		t.Errorf("expected no promotion, got %q", promoted)
	}
	if _, err := os.Stat(filepath.Join(dir, "memo.txt")); !os.IsNotExist(err) {
		t.Error("canonical transcript should not exist")
	}
}

func TestPromoteLiveTranscriptEmpty(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "memo.ogg")
	if err := os.WriteFile(filepath.Join(dir, "memo-live.txt"), []byte("  \n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	promoted, err := promoteLiveTranscript(audio)
	if err != nil {
		t.Fatalf("expected nil error for blank live file, got %v", err)
	}
	if promoted != "" {
		t.Errorf("expected skip for blank live file, got %q", promoted)
	}
}
