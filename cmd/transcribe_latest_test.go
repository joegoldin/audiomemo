package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFindLatestAudio(t *testing.T) {
	dir := t.TempDir()

	// Create files with staggered modification times.
	older := filepath.Join(dir, "recording-old.ogg")
	newer := filepath.Join(dir, "recording-new.ogg")
	os.WriteFile(older, []byte("old"), 0644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(newer, []byte("new"), 0644)

	got, err := findLatestAudio(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != newer {
		t.Errorf("expected %s, got %s", newer, got)
	}
}

func TestFindLatestAudioSkipsNonAudio(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "recording.ogg"), []byte("audio"), 0644)
	time.Sleep(10 * time.Millisecond)
	// This is newer but not an audio file.
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("text"), 0644)

	got, err := findLatestAudio(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "recording.ogg" {
		t.Errorf("expected recording.ogg, got %s", filepath.Base(got))
	}
}

func TestFindLatestAudioEmpty(t *testing.T) {
	dir := t.TempDir()
	_, err := findLatestAudio(dir)
	if err == nil {
		t.Error("expected error for empty directory")
	}
}

func TestFindLatestAudioMissingDir(t *testing.T) {
	_, err := findLatestAudio("/nonexistent/path")
	if err == nil {
		t.Error("expected error for missing directory")
	}
}

func TestRenameWithLabel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recording-2025-02-25T12-00-00.ogg")
	os.WriteFile(path, []byte("audio"), 0644)

	got, err := renameWithLabel(path, "standup")
	if err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(dir, "recording-2025-02-25T12-00-00-standup.ogg")
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("renamed file should exist: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("original file should no longer exist")
	}
}

func TestRenameWithLabelSpaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recording.ogg")
	os.WriteFile(path, []byte("audio"), 0644)

	got, err := renameWithLabel(path, "team meeting")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(got), "team-meeting") {
		t.Errorf("spaces should be replaced with hyphens, got %s", filepath.Base(got))
	}
}

func TestRenameWithLabelTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recording.ogg")
	os.WriteFile(path, []byte("audio"), 0644)

	longName := strings.Repeat("a", 300)
	got, err := renameWithLabel(path, longName)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(got)
	if len(base) > maxFilenameLen {
		t.Errorf("filename should be <= %d bytes, got %d: %s", maxFilenameLen, len(base), base)
	}
	if !strings.HasSuffix(base, ".ogg") {
		t.Errorf("extension should be preserved, got %s", base)
	}
}

func TestRenameWithLabelEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recording.ogg")
	os.WriteFile(path, []byte("audio"), 0644)

	got, err := renameWithLabel(path, "")
	if err != nil {
		t.Fatal(err)
	}
	// Empty label after trim → "recording-.ogg", but that's fine
	if _, err := os.Stat(got); err != nil {
		t.Errorf("file should exist: %v", err)
	}
}
