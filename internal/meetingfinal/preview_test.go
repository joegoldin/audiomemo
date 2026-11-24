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
		sources := []sessiontypes.Source{{ID: "001", Role: "us"}, {ID: "002", Role: "us"}, {ID: "003", Role: "us"}}
		Preview(ctx, feed, sources, Options{Session: "example", ASR: func(ctx context.Context, _ string, opts transcribe.TranscribeOpts) (*transcribe.Result, error) {
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
			if p.Session != "example" || p.Split {
				t.Fatalf("unexpected one-role preview: %+v", p)
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

func TestMixedMeetingPreviewUsesConfiguredRolesAndIsolatesDrops(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	feed := make(chan sessiontypes.PCM)
	updates := make(chan sessiontypes.Preview, 10)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	// The first track is Them, regardless of its position or ID. A second Them
	// track shares that lane, but not its PCM window.
	sources := []sessiontypes.Source{{ID: "001", Role: "them"}, {ID: "002", Role: "us"}, {ID: "003", Role: "them"}}
	var first sync.Once
	go func() {
		defer close(done)
		Preview(ctx, feed, sources, Options{Session: "mixed", ASR: func(ctx context.Context, _ string, _ transcribe.TranscribeOpts) (*transcribe.Result, error) {
			text := "local words"
			first.Do(func() {
				text = "remote words"
				close(started)
			})
			select {
			case <-release:
				return &transcribe.Result{Text: text}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}, func(p sessiontypes.Preview) { updates <- p })
	}()
	next := func() sessiontypes.Preview {
		t.Helper()
		select {
		case p := <-updates:
			if !p.Split || p.Session != "mixed" || p.Stable != "" || p.Tentative != "" {
				t.Fatalf("unexpected split preview: %+v", p)
			}
			return p
		case <-ctx.Done():
			t.Fatal(ctx.Err())
			return sessiontypes.Preview{}
		}
	}
	if p := next(); p.Us.Stable != "" || p.Them.Stable != "" {
		t.Fatalf("initial snapshot should announce two empty lanes: %+v", p)
	}
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
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	send("002") // queued while Them is running
	send("003") // no free queue slot: only Them should be marked behind
	if p := next(); p.Us.Tentative != "" || p.Them.Tentative != "Draft behind" {
		t.Fatalf("drop leaked across roles: %+v", p)
	}
	close(release)
	if p := next(); p.Them.Stable != "remote words" || p.Us.Stable != "" {
		t.Fatalf("Them result went to the wrong lane: %+v", p)
	}
	if p := next(); p.Them.Stable != "remote words" || p.Us.Stable != "local words" || p.Us.Tentative != "" {
		t.Fatalf("Us result went to the wrong lane: %+v", p)
	}
	close(feed)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
