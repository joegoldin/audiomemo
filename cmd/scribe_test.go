package cmd

import (
	"bytes"
	"io"
	"testing"
)

// The in-memory copy must be the whole recording even when the preview stops
// reading partway, as it does when a local model fails to start.
func TestPCMTapKeepsAudioAfterPreviewCloses(t *testing.T) {
	src, feed := io.Pipe()
	tap, preview := tapPCM(src)

	first := bytes.Repeat([]byte{1}, 1000)
	go feed.Write(first)
	got := make([]byte, len(first))
	if _, err := io.ReadFull(preview, got); err != nil {
		t.Fatal(err)
	}
	preview.Close()

	rest := bytes.Repeat([]byte{2}, 5000)
	feed.Write(rest)
	feed.Close()

	if pcm := tap.PCM(); !bytes.Equal(pcm, append(first, rest...)) {
		t.Errorf("kept %d bytes, want %d", len(pcm), len(first)+len(rest))
	}
}
