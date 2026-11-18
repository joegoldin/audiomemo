package meetingcapture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
)

func sources() []sessiontypes.Source {
	return []sessiontypes.Source{{ID: "001", Device: "mic", Role: "us"}, {ID: "002", Device: "monitor", Role: "them"}}
}

func TestCaptureReopensIndependently(t *testing.T) {
	var opens [2]atomic.Int32
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := Start(ctx, t.TempDir(), "combo", sources(), func(ctx context.Context, s sessiontypes.Source) (Stream, error) {
		index := int(s.ID[2] - '1')
		attempt := opens[index].Add(1)
		r, w := io.Pipe()
		go func() {
			defer w.Close()
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			rate := 8000 + index*8000
			for n := 0; ; n++ {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
				if _, err := w.Write(make([]byte, rate/100*2)); err != nil {
					return
				}
				if index == 1 && attempt == 1 && n == 4 {
					return
				}
			}
		}()
		return Stream{PCM: r, Rate: 8000 + index*8000, Channels: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Ready():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var resumed bool
	for !resumed {
		select {
		case p := <-c.Preview():
			resumed = p.Track == "002" && opens[1].Load() >= 2
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if _, err := c.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if opens[0].Load() != 1 || opens[1].Load() != 2 {
		t.Fatalf("unexpected reopen counts: %d, %d", opens[0].Load(), opens[1].Load())
	}
	if err := WithFinalization(c.Bundle(), func(p *Publication) error {
		m := p.Manifest()
		if len(m.Tracks[1].Files) != 2 || len(m.Tracks[1].Gaps) == 0 || len(m.Tracks[0].Files) != 1 || m.Tracks[1].Rate != 16000 {
			t.Fatalf("tracks: %+v", m.Tracks)
		}
		for _, track := range m.Tracks {
			for _, name := range track.Files {
				f, err := p.Open(name)
				if err != nil {
					t.Fatal(err)
				}
				st, err := f.Stat()
				f.Close()
				if err != nil || st.Mode().Perm() != 0600 || st.Size() <= 44 {
					t.Fatalf("bad source info: %v / %v", st, err)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPendingAndUnavailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := Start(ctx, t.TempDir(), "x", sources(), func(context.Context, sessiontypes.Source) (Stream, error) { return Stream{}, errors.New("unavailable") })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Ready():
		t.Fatal("unavailable capture became ready")
	default:
	}
	if err := WithFinalization(c.Bundle(), func(*Publication) error { return nil }); err == nil {
		t.Fatal("finalized live bundle")
	}
	if bundle, err := c.Stop(ctx); err == nil || bundle == "" {
		t.Fatalf("stop = %q, %v", bundle, err)
	}
	if _, err := os.Stat(c.Bundle() + "/manifest.json"); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverOnlyFramesOnDiskAfterCrash(t *testing.T) {
	now := time.Now().Add(-time.Hour)
	bundle, root := fixtureBundle(t, now)
	file := "tracks/001-000000-000000-120-8000-1.wav"
	content := append(header(8000, 1, 480000), make([]byte, 801)...)
	if err := os.WriteFile(bundle+"/"+file, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := WithFinalization(bundle, func(p *Publication) error {
		m := p.Manifest()
		spans := p.Segments("001")
		if m.State != "interrupted" || m.DurationMS != 170 || m.Jobs["capture"].State != "interrupted" || len(spans) != 1 || spans[0].Frames != 400 || m.EndedUTC != now.Add(170*time.Millisecond).UTC().Format(time.RFC3339Nano) {
			t.Fatalf("manifest=%+v spans=%+v", m, spans)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := root.ReadFile(file)
	if err != nil || !bytes.Equal(content, got) {
		t.Fatalf("original was rewritten: %v", err)
	}
}

func TestRetryRejectsEscapingOriginalAndManifestPaths(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		bundle, root := fixtureBundle(t, time.Now())
		target := t.TempDir() + "/outside.wav"
		if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := root.Symlink(target, "tracks/001-000000-000000-0-8000-1.wav"); err != nil {
			t.Fatal(err)
		}
		if err := WithFinalization(bundle, func(*Publication) error { t.Fatal("followed source symlink"); return nil }); err == nil {
			t.Fatal("accepted symlink")
		}
	})
	t.Run("traversal", func(t *testing.T) {
		bundle, root := fixtureBundle(t, time.Now())
		m, err := readManifest(root, filepath.Base(bundle))
		if err != nil {
			t.Fatal(err)
		}
		m.Tracks[0].Files = []string{"tracks/../../outside.wav"}
		if err := writeManifest(root, &m); err != nil {
			t.Fatal(err)
		}
		if err := WithFinalization(bundle, func(*Publication) error { t.Fatal("accepted traversal"); return nil }); err == nil {
			t.Fatal("accepted manifest path")
		}
	})
}

func TestPublicationResumesDurableReceiptWithoutRewriting(t *testing.T) {
	bundle, root := fixtureBundle(t, time.Now())
	if err := WithFinalization(bundle, func(p *Publication) error {
		name, f, err := newTemp(root)
		if err != nil {
			return err
		}
		data := []byte("durably written artifact")
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
		if err := finishFile(f); err != nil {
			return err
		}
		p.manifest.Artifacts = map[string]Artifact{"mix.wav": {SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Temp: name}}
		return writeManifest(root, &p.manifest)
	}); err != nil {
		t.Fatal(err)
	}
	if err := WithFinalization(bundle, func(p *Publication) error {
		return p.Publish("mix.wav", func(*os.File) error { t.Fatal("regenerated already durable artifact"); return nil })
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := root.ReadFile("mix.wav"); err != nil || string(got) != "durably written artifact" {
		t.Fatalf("artifact=%q %v", got, err)
	}
}

func fixtureBundle(t *testing.T, now time.Time) (string, *os.Root) {
	t.Helper()
	bundle := t.TempDir() + "/meetings/20260101T000000Z-AAAAAAAAAAAAAAAAAAAAAAAAAA"
	if err := os.MkdirAll(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(bundle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	lock, err := prepareBundle(root, filepath.Base(bundle), "combo", now, sources())
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	return bundle, root
}

func TestSegmentAnchorsHandleClockDriftAcrossTenMinutes(t *testing.T) {
	bundle, _ := fixtureBundle(t, time.Now())
	expected := map[string][]int64{}
	for i, src := range sources() {
		rate := 8000 + i*8000
		for seq := range 20 {
			epoch := 0
			offset := int64(seq*30000 + i*(120+seq*12))
			if i == 1 && seq >= 10 {
				epoch = 1
				offset += 500
			}
			file := fmt.Sprintf("%s/tracks/%s-%06d-%06d-%d-%d-1.wav", bundle, src.ID, epoch, seq, offset, rate)
			frames := rate * 30
			f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(header(rate, 1, uint32(frames*2))); err != nil {
				t.Fatal(err)
			}
			if err := f.Truncate(WAVHeaderSize + int64(frames*2)); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			expected[src.ID] = append(expected[src.ID], offset)
		}
	}
	if err := WithFinalization(bundle, func(p *Publication) error {
		for _, s := range sources() {
			spans := p.Segments(s.ID)
			if len(spans) != 20 {
				t.Fatalf("track %s: %d spans", s.ID, len(spans))
			}
			for i, span := range spans {
				if span.StartMS != expected[s.ID][i] {
					t.Fatalf("track %s span %d start %d want %d", s.ID, i, span.StartMS, expected[s.ID][i])
				}
				if i+1 < len(spans) && span.epoch == spans[i+1].epoch && span.EndMS != spans[i+1].StartMS {
					t.Fatalf("track %s gap between contiguous spans", s.ID)
				}
			}
		}
		gaps := p.Manifest().Tracks[1].Gaps
		if len(gaps) != 2 || gaps[1].EndMS-gaps[1].StartMS != 512 {
			t.Fatalf("wrong gaps: %+v", gaps)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClockAnchorFiltersSchedulingOutliers(t *testing.T) {
	var clock segmentClock
	for _, prediction := range []int64{30_012, 30_011, 30_055, 30_013, 29_990} {
		clock.observe(prediction)
	}
	if got := clock.boundary(30_000); got != 30_012 {
		t.Fatalf("corrected boundary = %d", got)
	}
	clock = segmentClock{}
	for _, prediction := range []int64{29_012, 29_011, 29_055, 29_013, 29_990} {
		clock.observe(prediction)
	}
	if got := clock.boundary(30_000); got != 30_000 {
		t.Fatalf("untrustworthy boundary = %d", got)
	}
}

func TestPublicationDiskFailureLeavesOriginalAndNoDerivedFile(t *testing.T) {
	bundle, root := fixtureBundle(t, time.Now())
	file := "tracks/001-000000-000000-0-8000-1.wav"
	original := append(header(8000, 1, 320), make([]byte, 320)...)
	if err := os.WriteFile(bundle+"/"+file, original, 0600); err != nil {
		t.Fatal(err)
	}
	err := WithFinalization(bundle, func(p *Publication) error {
		return p.Publish("mix.wav", func(out *os.File) error {
			if _, err := out.Write([]byte("partial output")); err != nil {
				return err
			}
			return syscall.ENOSPC
		})
	})
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("write failure: %v", err)
	}
	if _, err := root.Stat("mix.wav"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published incomplete mix: %v", err)
	}
	if got, err := root.ReadFile(file); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("original changed: %v", err)
	}
}
