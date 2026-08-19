package cmd

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePrintMode(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    printMode
		wantErr bool
	}{
		{name: "empty defaults to auto", in: "", want: printAuto},
		{name: "auto", in: "auto", want: printAuto},
		{name: "path", in: "path", want: printPath},
		{name: "text", in: "text", want: printText},
		{name: "both", in: "both", want: printBoth},
		{name: "none", in: "none", want: printNone},
		{name: "case insensitive", in: "TEXT", want: printText},
		{name: "unknown value rejected", in: "transcript", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePrintMode(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parsePrintMode(%q) = %q, want error", tt.in, got)
				}
				// The error has to list the alternatives; a bare "invalid"
				// leaves the user guessing.
				if !strings.Contains(err.Error(), "path") || !strings.Contains(err.Error(), "text") {
					t.Errorf("error does not list valid values: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parsePrintMode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolvePrintMode(t *testing.T) {
	tests := []struct {
		name        string
		mode        printMode
		stdoutIsTTY bool
		clips       bool
		want        printMode
	}{
		{name: "auto on a terminal prints the path", mode: printAuto, stdoutIsTTY: true, want: printPath},
		{name: "auto down a pipe prints the transcript", mode: printAuto, want: printText},
		{name: "auto in clips mode prints paths even piped", mode: printAuto, clips: true, want: printPath},
		{name: "auto in clips mode on a terminal prints paths", mode: printAuto, stdoutIsTTY: true, clips: true, want: printPath},
		{name: "explicit path is honoured down a pipe", mode: printPath, want: printPath},
		{name: "explicit text is honoured on a terminal", mode: printText, stdoutIsTTY: true, want: printText},
		{name: "both is unchanged", mode: printBoth, want: printBoth},
		{name: "none is unchanged", mode: printNone, stdoutIsTTY: true, want: printNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvePrintMode(tt.mode, tt.stdoutIsTTY, tt.clips)
			if got != tt.want {
				t.Errorf("resolvePrintMode(%q, tty=%v, clips=%v) = %q, want %q",
					tt.mode, tt.stdoutIsTTY, tt.clips, got, tt.want)
			}
		})
	}
}

func TestValidatePrintFlags(t *testing.T) {
	tests := []struct {
		name       string
		changed    bool
		mode       printMode
		streamFlag bool
		clips      bool
		wantErr    string
	}{
		{name: "default auto is always fine"},
		{name: "auto with stream is fine", streamFlag: true},
		{name: "auto with clips is fine", clips: true},
		{name: "explicit print with stream is rejected", changed: true, mode: printText, streamFlag: true, wantErr: "--stream"},
		{name: "explicit none with stream is rejected", changed: true, mode: printNone, streamFlag: true, wantErr: "--stream"},
		{name: "text with clips is rejected", changed: true, mode: printText, clips: true, wantErr: "--clips"},
		{name: "both with clips is rejected", changed: true, mode: printBoth, clips: true, wantErr: "--clips"},
		{name: "path with clips is fine", changed: true, mode: printPath, clips: true},
		{name: "none with clips is fine", changed: true, mode: printNone, clips: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePrintFlags(tt.changed, tt.mode, tt.streamFlag, tt.clips)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestResolveStdoutText(t *testing.T) {
	transcript := filepath.Join("/tmp", "rec.txt")
	readOK := func(string) ([]byte, error) { return []byte("promoted live text\n"), nil }
	readMissing := func(string) ([]byte, error) { return nil, fs.ErrNotExist }
	readBlank := func(string) ([]byte, error) { return []byte("  \n\n"), nil }
	readBroken := func(string) ([]byte, error) { return nil, errors.New("disk on fire") }

	tests := []struct {
		name      string
		batchText string
		readFile  func(string) ([]byte, error)
		want      string
	}{
		{
			name:      "batch text wins over the live transcript",
			batchText: "batch text",
			readFile:  readOK,
			want:      "batch text",
		},
		{
			name:     "falls back to the promoted transcript",
			readFile: readOK,
			want:     "promoted live text",
		},
		{
			name:      "blank batch text falls back",
			batchText: "   \n",
			readFile:  readOK,
			want:      "promoted live text",
		},
		{
			name:     "no transcript at all yields nothing",
			readFile: readMissing,
			want:     "",
		},
		{
			name:     "blank transcript yields nothing",
			readFile: readBlank,
			want:     "",
		},
		{
			name:     "unreadable transcript yields nothing",
			readFile: readBroken,
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveStdoutText(tt.batchText, transcript, tt.readFile)
			if got != tt.want {
				t.Errorf("resolveStdoutText() = %q, want %q", got, tt.want)
			}
		})
	}
}
