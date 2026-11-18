package meetingfinal

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/joegoldin/audiomemo/internal/transcribe"
)

func TestPreviewRendersBoundedSourceTaggedDraftAndSignalsDrops(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	feed := make(chan sessiontypes.PCM)
	snapshots := make(chan sessiontypes.Preview, 10)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	var first sync.Once
	go func() {
		defer close(done)
		Preview(ctx, feed, Options{Session: "example", ASR: func(ctx context.Context, _ string, opts transcribe.TranscribeOpts) (*transcribe.Result, error) {
			if opts.Diarize || opts.Model == "" {
				t.Errorf("bad draft options: %+v", opts)
			}
			first.Do(func() { close(entered) })
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
			}
			return &transcribe.Result{Text: "sample words"}, nil
		}}, func(p sessiontypes.Preview) { snapshots <- p })
	}()
	send := func(id string) {
		t.Helper()
		for n := range draftSeconds {
			select {
			case feed <- sessiontypes.PCM{Track: id, StartMS: int64(n * 1000), Rate: 8000, Channels: 1, S16LE: make([]byte, 16000)}:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	send("001")
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	send("002")
	send("003")
	close(release)
	var behind, recognized, queued bool
	for !behind || !recognized || !queued {
		select {
		case p := <-snapshots:
			if p.Session != "example" {
				t.Fatalf("session = %s", p.Session)
			}
			behind = behind || strings.Contains(p.Tentative, "behind")
			recognized = recognized || strings.Contains(p.Stable, "001: sample words")
			queued = queued || strings.Contains(p.Stable, "002: sample words")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(feed)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
