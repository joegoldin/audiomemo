package record

import (
	"context"
	"encoding/binary"
	"io"
	"os/exec"
	"testing"
	"time"
)

func TestMeetingFFmpegUsesOneSyntheticIngress(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	s, err := openMeetingFFmpeg(ctx, []string{"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=8000:duration=0.03"}, 8000, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.PCM.Close()
	if s.Rate != 8000 || s.Channels != 1 {
		t.Fatalf("format=%+v", s)
	}
	b, err := io.ReadAll(s.PCM)
	if err != nil || len(b) != 480 {
		t.Fatalf("PCM bytes=%d %v", len(b), err)
	}
	if value := binary.LittleEndian.Uint16(b[2:]); value == 0 {
		t.Fatal("source was not audible")
	}
}

func TestMeetingFFmpegCancellationClosesSyntheticIngress(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	s, err := openMeetingFFmpeg(ctx, []string{"-re", "-f", "lavfi", "-i", "sine=sample_rate=8000"}, 8000, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.PCM.Close()
	if _, err := io.ReadFull(s.PCM, make([]byte, 160)); err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- s.PCM.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("synthetic FFmpeg did not close promptly")
	}
}
