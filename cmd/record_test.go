package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveRecordTranscriptionMode(t *testing.T) {
	tests := []struct {
		name                string
		noLiveFlag          bool
		whisperShortcut     bool
		transcribeFlag      bool
		wantLiveDisabled    bool
		wantBatchTranscribe bool
	}{
		{name: "ordinary recording keeps defaults"},
		{
			name:             "no-live flag disables live transcription",
			noLiveFlag:       true,
			wantLiveDisabled: true,
		},
		{
			name:                "recw disables live and enables batch",
			whisperShortcut:     true,
			wantLiveDisabled:    true,
			wantBatchTranscribe: true,
		},
		{
			name:                "transcribe flag enables batch",
			transcribeFlag:      true,
			wantBatchTranscribe: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			liveDisabled, batchTranscribe := resolveRecordTranscriptionMode(
				tt.noLiveFlag,
				tt.whisperShortcut,
				tt.transcribeFlag,
			)
			if liveDisabled != tt.wantLiveDisabled {
				t.Errorf("liveDisabled = %v, want %v", liveDisabled, tt.wantLiveDisabled)
			}
			if batchTranscribe != tt.wantBatchTranscribe {
				t.Errorf("batchTranscribe = %v, want %v", batchTranscribe, tt.wantBatchTranscribe)
			}
		})
	}
}

func TestBuildPostTranscribeArgsRecwPrefersWhisperCPPAndForcesLocal(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "whisper-cli" {
			return "/usr/bin/whisper-cli", nil
		}
		return "", errors.New("not found")
	}

	got, err := buildPostTranscribeArgs(
		"memo.ogg",
		"--language en --backend elevenlabs",
		false,
		true,
		lookPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--language", "en",
		"--backend", "elevenlabs",
		"--backend", "whisper-cpp",
		"memo.ogg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestBuildPostTranscribeArgsRecwFallsBackToWhisper(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "whisper" {
			return "/usr/bin/whisper", nil
		}
		return "", errors.New("not found")
	}

	got, err := buildPostTranscribeArgs("memo.ogg", "", true, true, lookPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--verbose", "--backend", "whisper", "memo.ogg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestBuildPostTranscribeArgsRecwRequiresLocalWhisper(t *testing.T) {
	lookPath := func(string) (string, error) {
		return "", errors.New("not found")
	}

	_, err := buildPostTranscribeArgs("memo.ogg", "", false, true, lookPath)
	if err == nil {
		t.Fatal("expected an error when neither whisper-cli nor whisper is available")
	}
}

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

func TestValidateStreamFlags(t *testing.T) {
	if err := validateStreamFlags(false, true, true); err != nil {
		t.Errorf("without --stream nothing is rejected, got %v", err)
	}
	if err := validateStreamFlags(true, false, false); err != nil {
		t.Errorf("plain --stream must be accepted, got %v", err)
	}
	// Clips mode drives an interactive TUI loop, so it owns stdout for
	// something that is not NDJSON.
	err := validateStreamFlags(true, true, false)
	if err == nil {
		t.Fatal("--stream --clips must be rejected")
	}
	if !strings.Contains(err.Error(), "--clips") {
		t.Errorf("error should name the offending flag, got %q", err)
	}
	// --list-devices prints a human table on stdout.
	err = validateStreamFlags(true, false, true)
	if err == nil {
		t.Fatal("--stream --list-devices must be rejected")
	}
	if !strings.Contains(err.Error(), "device list") {
		t.Errorf("error should point at the supported alternative, got %q", err)
	}
}

func TestResolveStreamMode(t *testing.T) {
	cases := []struct {
		name                  string
		liveActive, willBatch bool
		want                  string
	}{
		{"realtime backend connected", true, false, "live"},
		{"realtime plus a batch pass still yields partials", true, true, "live"},
		{"no key but -t was passed", false, true, "batch"},
		{"no key and no batch pass", false, false, "none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveStreamMode(c.liveActive, c.willBatch); got != c.want {
				t.Errorf("resolveStreamMode(%v, %v) = %q, want %q", c.liveActive, c.willBatch, got, c.want)
			}
		})
	}
}

func TestReportLiveUnavailable(t *testing.T) {
	const note = "live transcription unavailable: no ElevenLabs API key configured"
	tests := []struct {
		name         string
		note         string
		streaming    bool
		liveDisabled bool
		want         bool
	}{
		{name: "missing key while streaming is worth an error", note: note, streaming: true, want: true},
		{
			name:         "an explicit --no-live-transcription is not an error",
			note:         "live transcription disabled",
			streaming:    true,
			liveDisabled: true,
		},
		{name: "no note means nothing to report", streaming: true},
		{name: "outside --stream there is no consumer to tell", note: note},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := reportLiveUnavailable(tc.note, tc.streaming, tc.liveDisabled); got != tc.want {
				t.Errorf("reportLiveUnavailable(%q, %v, %v) = %v, want %v",
					tc.note, tc.streaming, tc.liveDisabled, got, tc.want)
			}
		})
	}
}
