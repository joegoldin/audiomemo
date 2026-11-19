package transcribe

import (
	"github.com/joegoldin/audiomemo/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSortSourceTurnsWithoutInterleavingOverlappingWords(t *testing.T) {
	got, e := joinSides([]byte{1, 0, 2, 0}, func(p []byte) (*Result, error) {
		if p[0] == 1 {
			return &Result{Text: "later answer", Words: []Word{{Word: "later", Start: 1, End: 1.3}, {Word: "answer", Start: 1.4, End: 2}}}, nil
		}
		return &Result{Text: "first speaker", Words: []Word{{Word: "first", Start: .5, End: 1}, {Word: "speaker", Start: 1.1, End: 1.5}}}, nil
	})
	if e != nil || got != "Them: first speaker\nUs: later answer" {
		t.Fatal(got, e)
	}
}
func TestNativeSourceAttributedFinal(t *testing.T) {
	bin := os.Getenv("AUDIOMEMO_NEMO_TEST_BINARY")
	if bin == "" {
		t.Skip("native model not requested")
	}
	cfg := config.Default().Transcribe.Nemo
	cfg.Binary = bin
	cfg.Device = "cpu"
	pcm, e := exec.Command("ffmpeg", "-v", "error", "-i", filepath.Join("..", "..", "testdata", "test.ogg"), "-ar", "16000", "-ac", "1", "-f", "s16le", "pipe:1").Output()
	if e != nil {
		t.Fatal(e)
	}
	stereo := make([]byte, len(pcm)*4)
	for i := 0; i < len(pcm); i += 2 {
		copy(stereo[2*i:], pcm[i:i+2])
		copy(stereo[2*len(pcm)+2*i+2:], pcm[i:i+2])
	}
	txt, e := NewNemo(cfg).TranscribeSides(t.Context(), stereo, "")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(txt, "Us:") || !strings.Contains(txt, "\nThem:") {
		t.Fatal("missing source turns")
	}
}
