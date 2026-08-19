package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestResolveMaxDuration(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		alias   string
		want    time.Duration
		wantErr string
	}{
		{name: "both unset"},
		{name: "max-duration parses", flag: "30s", want: 30 * time.Second},
		{name: "compound duration parses", flag: "1h30m", want: 90 * time.Minute},
		{name: "deprecated duration still works", alias: "5m", want: 5 * time.Minute},
		{name: "max-duration wins over the alias", flag: "10s", alias: "5m", want: 10 * time.Second},
		{name: "bare number is rejected", flag: "30", wantErr: "--max-duration"},
		{name: "garbage is rejected", flag: "soon", wantErr: "--max-duration"},
		{name: "bad alias names the flag the user typed", alias: "soon", wantErr: "--duration"},
		{name: "negative is rejected", flag: "-5s", wantErr: "--max-duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveMaxDuration(tt.flag, tt.alias)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error mentioning %q, got %v", tt.wantErr, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveMaxDuration(%q, %q) = %v, want %v", tt.flag, tt.alias, got, tt.want)
			}
		})
	}
}

func TestParseMaxSilence(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "unset"},
		{name: "seconds", in: "5s", want: 5 * time.Second},
		{name: "sub-second", in: "800ms", want: 800 * time.Millisecond},
		{name: "bare number rejected", in: "5", wantErr: true},
		{name: "negative rejected", in: "-5s", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMaxSilence(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %v", got)
				}
				if !strings.Contains(err.Error(), "--max-silence") {
					t.Errorf("error %q does not name the flag", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseMaxSilence(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestHeadlessStopHint(t *testing.T) {
	tests := []struct {
		name        string
		maxDuration time.Duration
		maxSilence  time.Duration
		want        string
	}{
		{
			name: "no stop condition leaves the signal",
			want: "Ctrl+C to stop",
		},
		{
			name:        "duration only",
			maxDuration: 30 * time.Second,
			want:        "stopping after 30s, or Ctrl+C",
		},
		{
			name:       "silence only",
			maxSilence: 5 * time.Second,
			want:       "stopping after 5s of silence, or Ctrl+C",
		},
		{
			name:        "both conditions, whichever comes first",
			maxDuration: time.Minute,
			maxSilence:  2 * time.Second,
			want:        "stopping after 1m0s or 2s of silence, or Ctrl+C",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := headlessStopHint(tt.maxDuration, tt.maxSilence)
			if got != tt.want {
				t.Errorf("headlessStopHint(%v, %v) = %q, want %q", tt.maxDuration, tt.maxSilence, got, tt.want)
			}
		})
	}
}

func TestWantsStdoutText(t *testing.T) {
	tests := []struct {
		mode printMode
		want bool
	}{
		{mode: printText, want: true},
		{mode: printBoth, want: true},
		{mode: printPath, want: false},
		{mode: printNone, want: false},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			if got := wantsStdoutText(tt.mode); got != tt.want {
				t.Errorf("wantsStdoutText(%q) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

func TestBatchNeededForText(t *testing.T) {
	tests := []struct {
		name       string
		mode       printMode
		liveActive bool
		already    bool
		want       bool
	}{
		{
			name: "path mode never forces a batch pass",
			mode: printPath,
		},
		{
			name:       "text with live transcription uses the live transcript",
			mode:       printText,
			liveActive: true,
		},
		{
			name: "text without live transcription needs a batch pass",
			mode: printText,
			want: true,
		},
		{
			name: "both without live transcription needs a batch pass",
			mode: printBoth,
			want: true,
		},
		{
			name:    "already transcribing stays as it was",
			mode:    printText,
			already: true,
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := batchNeededForText(tt.mode, tt.liveActive, tt.already)
			if got != tt.want {
				t.Errorf("batchNeededForText(%q, live=%v, already=%v) = %v, want %v",
					tt.mode, tt.liveActive, tt.already, got, tt.want)
			}
		})
	}
}
