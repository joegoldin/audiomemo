package meetingfinal

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/meetingcapture"
	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/joegoldin/audiomemo/internal/transcribe"
)

func TestFinalizeKeepsIndependentTrackIdentityAndRetries(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	c, err := meetingcapture.Start(ctx, t.TempDir(), "fixture", []sessiontypes.Source{{ID: "001", Device: "one", Role: "us"}, {ID: "002", Device: "two", Role: "them"}}, func(ctx context.Context, source sessiontypes.Source) (meetingcapture.Stream, error) {
		r, w := io.Pipe()
		go func() {
			defer w.Close()
			value := uint16(source.ID[2]) * 100
			block := make([]byte, 320)
			for i := 0; i < len(block); i += 2 {
				binary.LittleEndian.PutUint16(block[i:], value)
			}
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				if _, err := w.Write(block); err != nil {
					return
				}
			}
		}()
		return meetingcapture.Stream{PCM: r, Rate: 8000, Channels: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for seen["001"] < 5 || seen["002"] < 5 {
		select {
		case p := <-c.Preview():
			seen[p.Track]++
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	path, err := c.Stop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	options := Options{Format: "srt", ASR: func(_ context.Context, path string, opts transcribe.TranscribeOpts) (*transcribe.Result, error) {
		calls++
		if !opts.Diarize {
			t.Error("expected diarization")
		}
		input, err := os.ReadFile(path)
		if err != nil || len(input) < 46 {
			t.Fatalf("read ASR wave: %v; bytes=%d", err, len(input))
		}
		value := binary.LittleEndian.Uint16(input[44:])
		expected := uint16('1') * 100
		if calls > 1 {
			expected = uint16('2') * 100
		}
		if value != expected {
			t.Fatalf("ASR did not receive independent audio: got %d want %d", value, expected)
		}
		if calls == 2 {
			return nil, errors.New("synthetic model failure")
		}
		return &transcribe.Result{Text: "same words", Words: []transcribe.Word{{Word: "same", Start: .02, End: .04, Speaker: "speaker_0"}, {Word: "words", Start: .045, End: .06, Speaker: "speaker_0"}}, SpeakerTurns: []transcribe.SpeakerTurn{{Start: 0, End: .041, Speaker: "speaker_0"}, {Start: .02, End: .041, Speaker: "speaker_1"}}}, nil
	}}
	if text, err := Finalize(ctx, path, options); err == nil || text != "" {
		t.Fatalf("failed final = %q, %v", text, err)
	}
	if _, err := os.Stat(filepath.Join(path, "mix.wav")); err != nil {
		t.Fatal(err)
	}
	text, err := Finalize(ctx, path, options)
	if err != nil || calls != 3 || strings.Count(text, "same") != 2 || strings.Count(text, "words") != 2 {
		t.Fatalf("retry = %q, %v; %d ASR calls", text, err, calls)
	}
	data, err := os.ReadFile(filepath.Join(path, "transcript.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Transcript
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	byTrack := map[string]int{}
	for _, turn := range got.Turns {
		if turn.Speaker == "speaker_0" {
			byTrack[turn.TrackID]++
		}
	}
	if len(got.Turns) != 4 || len(got.Words) != 4 || byTrack["001"] != 1 || byTrack["002"] != 1 {
		t.Fatalf("incorrect track-local turns: %+v", got)
	}
	for _, word := range got.Words {
		if word.Speaker != "" {
			t.Fatalf("assigned ambiguous or unsupported word: %+v", word)
		}
	}
	if output, err := Finalize(ctx, path, options); err != nil || text != output || calls != 3 {
		t.Fatalf("idempotent retry = %q %v, calls %d", output, err, calls)
	}
	edit := []byte("manual edit")
	if err := os.WriteFile(filepath.Join(path, "transcript.txt"), edit, 0600); err != nil {
		t.Fatal(err)
	}
	if text, err := Finalize(ctx, path, options); err == nil || text != "" {
		t.Fatalf("edited output = %q %v", text, err)
	}
	if edited, err := os.ReadFile(filepath.Join(path, "transcript.txt")); err != nil || string(edited) != string(edit) {
		t.Fatalf("edit overwritten: %q %v", edited, err)
	}
}
