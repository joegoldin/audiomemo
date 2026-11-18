package meetingfinal

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/meetingcapture"
	"github.com/joegoldin/audiomemo/internal/sessiontypes"
)

func TestMeetingNativeNemo(t *testing.T) {
	binary, tts := os.Getenv("AUDIOMEMO_NEMO_TEST_BINARY"), os.Getenv("AUDIOMEMO_MEETING_TTS_TEST_BINARY")
	if binary == "" || tts == "" {
		t.Skip("set AUDIOMEMO_NEMO_TEST_BINARY and AUDIOMEMO_MEETING_TTS_TEST_BINARY for synthetic native NeMo inference")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	pcm := map[string][]byte{}
	phrases := []string{"The blue bicycle is parked outside. Please remember to lock it before sunset.", "Today's agenda includes apples and oranges. Please bring the fruit to the kitchen."}
	ids := []string{"001", "002"}
	for i, id := range ids {
		wave := filepath.Join(dir, id+".wav")
		if output, err := exec.CommandContext(ctx, tts, "-v", []string{"en-us", "en-gb"}[i], "-w", wave, phrases[i]).CombinedOutput(); err != nil {
			t.Fatalf("generate speech: %v: %s", err, output)
		}
		output, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", wave, "-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:1").Output()
		if err != nil {
			t.Fatal(err)
		}
		pcm[id] = output
	}
	sourceDone := make(chan string, 2)
	var writers sync.WaitGroup
	c, err := meetingcapture.Start(ctx, dir, "synthetic", []sessiontypes.Source{{ID: ids[0], Device: "synthetic-1", Role: "us"}, {ID: ids[1], Device: "synthetic-2", Role: "them"}}, func(ctx context.Context, s sessiontypes.Source) (meetingcapture.Stream, error) {
		r, w := io.Pipe()
		writers.Go(func() {
			defer w.Close()
			blob := pcm[s.ID]
			// Send a whole number of capture blocks to avoid the test itself needing
			// an EOF/reopen to flush a final partial block.
			blob = append(blob, make([]byte, (640-len(blob)%640)%640)...)
			if _, err := w.Write(blob); err != nil {
				return
			}
			sourceDone <- s.ID
			<-ctx.Done()
		})
		return meetingcapture.Stream{PCM: r, Rate: 16000, Channels: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range ids {
		select {
		case <-sourceDone:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	bundle, err := c.Stop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writers.Wait()
	opts := Options{Nemo: config.Default().Transcribe.Nemo}
	opts.Nemo.Binary = binary
	if device := os.Getenv("AUDIOMEMO_NEMO_TEST_DEVICE"); device != "" {
		opts.Nemo.Device = device
	}
	text, err := Finalize(ctx, bundle, opts)
	if err != nil {
		t.Fatal(err)
	}
	if lower := strings.ToLower(text); !strings.Contains(lower, "bicycle") || !strings.Contains(lower, "oranges") {
		t.Fatalf("fixture keywords not recognized: %q", text)
	}
	data, err := os.ReadFile(filepath.Join(bundle, "transcript.json"))
	if err != nil {
		t.Fatal(err)
	}
	var transcript Transcript
	if err := json.Unmarshal(data, &transcript); err != nil {
		t.Fatal(err)
	}
	trackWords, trackTurns := map[string]int{}, map[string]int{}
	for _, w := range transcript.Words {
		trackWords[w.TrackID]++
	}
	for _, turn := range transcript.Turns {
		trackTurns[turn.TrackID]++
	}
	for _, id := range ids {
		if trackWords[id] == 0 || trackTurns[id] == 0 {
			t.Fatalf("track %s has missing words or speaker turns", id)
		}
	}
	t.Logf("synthetic local NeMo: track 001 %d words/%d turns; track 002 %d words/%d turns", trackWords["001"], trackTurns["001"], trackWords["002"], trackTurns["002"])
	t.Run("draft", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
		defer cancel()
		feed := make(chan sessiontypes.PCM, draftSeconds)
		out := make(chan sessiontypes.Preview, 4)
		opts.Session = "generated-draft"
		for second := range draftSeconds {
			from := second * recognitionRate * 2
			to := from + recognitionRate*2
			feed <- sessiontypes.PCM{Track: "001", StartMS: int64(second * 1000), Rate: recognitionRate, Channels: 1, S16LE: pcm["001"][from:to]}
		}
		go Preview(ctx, feed, opts, func(p sessiontypes.Preview) { out <- p })
		for {
			select {
			case p := <-out:
				if p.Session != opts.Session {
					t.Fatalf("unexpected draft session: %q", p.Session)
				}
				if strings.Contains(strings.ToLower(p.Stable), "bicycle") {
					return
				}
				if p.Stable != "" || strings.Contains(p.Tentative, "behind") {
					t.Fatalf("unexpected native draft: %+v", p)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	})
}
