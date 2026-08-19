package cmd

import "testing"

func TestChooseTTY(t *testing.T) {
	tests := []struct {
		name        string
		stdoutIsTTY bool
		devTTYOpen  bool
		stderrIsTTY bool
		want        ttyChoice
	}{
		{
			name:        "stdout is a terminal, render there as always",
			stdoutIsTTY: true,
			devTTYOpen:  true,
			stderrIsTTY: true,
			want:        ttyStdout,
		},
		{
			name:        "stdout piped, /dev/tty wins over stderr",
			devTTYOpen:  true,
			stderrIsTTY: true,
			want:        ttyDevTTY,
		},
		{
			name:        "stdout piped and no /dev/tty, fall back to stderr",
			stderrIsTTY: true,
			want:        ttyStderr,
		},
		{
			name: "nothing interactive at all",
			want: ttyNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chooseTTY(tt.stdoutIsTTY, tt.devTTYOpen, tt.stderrIsTTY)
			if got != tt.want {
				t.Errorf("chooseTTY(%v, %v, %v) = %v, want %v",
					tt.stdoutIsTTY, tt.devTTYOpen, tt.stderrIsTTY, got, tt.want)
			}
		})
	}
}
