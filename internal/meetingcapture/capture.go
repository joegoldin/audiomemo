package meetingcapture

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
)

const (
	sourceTimeout    = 4 * time.Second
	readinessTimeout = 10 * time.Second
	initialBackoff   = 200 * time.Millisecond
	previewQueue     = 32
)

// Stream carries interleaved signed 16-bit little-endian PCM. Close must
// unblock a concurrent Read; the opener must honor cancellation while opening.
type Stream struct {
	PCM            io.ReadCloser
	Rate, Channels int
}
type Opener func(context.Context, sessiontypes.Source) (Stream, error)

type Capture struct {
	bundle, id  string
	started     time.Time
	cancel      context.CancelFunc
	ready, done chan struct{}
	first       sync.Once
	preview     chan sessiontypes.PCM
	stopAt      atomic.Int64 // elapsed ns + 1; zero means not requested
	mu          sync.Mutex
	warnings    []string
	err         error // written before close(done)
}

// Start admits a capture and durably stores the selected source descriptors.
// Its context belongs to the capture owner, not the caller's IPC request.
func Start(ctx context.Context, outputRoot, group string, sources []sessiontypes.Source, open Opener) (*Capture, error) {
	if err := validateSources(sources); err != nil {
		return nil, err
	}
	if open == nil || outputRoot == "" {
		return nil, errors.New("meeting requires an audio opener and output directory")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	outputRoot, err := filepath.Abs(outputRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outputRoot, 0700); err != nil {
		return nil, err
	}
	output, err := os.OpenRoot(outputRoot)
	if err != nil {
		return nil, err
	}
	defer output.Close()
	if err := output.Mkdir("meetings", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := output.Lstat("meetings")
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errors.New("meetings directory must be private and not a symlink")
	}
	parent, err := output.OpenRoot("meetings")
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if opened, err := parent.Stat("."); err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("meetings directory changed while opening")
	}
	now := time.Now()
	id := now.UTC().Format("20060102T150405Z") + "-" + rand.Text()
	if err := parent.Mkdir(id, 0700); err != nil {
		return nil, err
	}
	root, err := parent.OpenRoot(id)
	if err != nil {
		return nil, err
	}
	lock, err := prepareBundle(root, id, group, now, sources)
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("prepare meeting bundle: %w", err)
	}
	if err := errors.Join(syncDir(parent), syncDir(output)); err != nil {
		lock.Close()
		root.Close()
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	c := &Capture{bundle: filepath.Join(outputRoot, "meetings", id), id: id, started: now, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), preview: make(chan sessiontypes.PCM, previewQueue), warnings: make([]string, len(sources))}
	sources = append([]sessiontypes.Source(nil), sources...)
	go c.run(life, root, lock, group, sources, open)
	return c, nil
}

func baseManifest(id, group string, now time.Time, sources []sessiontypes.Source) BundleManifest {
	m := BundleManifest{Manifest: sessiontypes.Manifest{Version: sessiontypes.Version, ID: id, Group: group, State: "capturing", StartedUTC: now.UTC().Format(time.RFC3339Nano), Tracks: make([]sessiontypes.Track, len(sources))}, PCMFormat: "signed 16-bit little-endian PCM; segmented WAV; per-segment rate and channel count in filename"}
	for i, s := range sources {
		m.Tracks[i].Source = s
	}
	return m
}

func prepareBundle(root *os.Root, id, group string, now time.Time, sources []sessiontypes.Source) (*os.File, error) {
	if err := root.Mkdir("tracks", 0700); err != nil {
		return nil, err
	}
	f, err := root.OpenFile(".lock", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	lock, err := lockBundle(root)
	if err != nil {
		return nil, err
	}
	m := baseManifest(id, group, now, sources)
	if err := writeManifest(root, &m); err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}

func (c *Capture) Bundle() string { return c.bundle }

// Ready closes on first stored audio; Done also closes if capture never starts.
func (c *Capture) Ready() <-chan struct{} { return c.ready }
func (c *Capture) Done() <-chan struct{}  { return c.done }

// Preview is a lossy channel for one consumer. Each frame owns its bytes.
func (c *Capture) Preview() <-chan sessiontypes.PCM { return c.preview }

func (c *Capture) Stop(ctx context.Context) (string, error) {
	c.requestStop()
	select {
	case <-c.done:
		return c.bundle, c.err
	case <-ctx.Done():
		return c.bundle, ctx.Err()
	}
}
func (c *Capture) requestStop() {
	c.stopAt.CompareAndSwap(0, int64(time.Since(c.started))+1)
	c.cancel()
}

func (c *Capture) warning(index int, message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.warnings[index] = message
}
func (c *Capture) Status() sessiontypes.Status {
	s := sessiontypes.Status{Capture: "pending", Session: c.id, StartedUTC: c.started.UTC().Format(time.RFC3339Nano)}
	select {
	case <-c.ready:
		s.Capture = "recording"
	default:
	}
	select {
	case <-c.done:
		s.Capture = "stopped"
		if c.err != nil {
			s.Warning = c.err.Error()
		}
	default:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, msg := range c.warnings {
		if msg != "" {
			s.Warning = appendError(s.Warning, msg)
		}
	}
	return s
}

func (c *Capture) run(ctx context.Context, root *os.Root, lock *os.File, group string, sources []sessiontypes.Source, open Opener) {
	defer close(c.done)
	defer close(c.preview)
	defer root.Close()
	defer lock.Close()
	defer c.cancel()
	tracks, err := openTracks(root)
	if err != nil {
		c.err = err
		return
	}
	defer tracks.Close()
	m := baseManifest(c.id, group, c.started, sources)
	timer := time.AfterFunc(readinessTimeout, func() {
		select {
		case <-c.ready:
		default:
			c.requestStop()
		}
	})
	defer timer.Stop()
	var workers sync.WaitGroup
	for i, s := range sources {
		workers.Go(func() { m.Tracks[i].Error = c.captureTrack(ctx, tracks, i, s, open) })
	}
	workers.Wait()
	closedAt := time.Since(c.started)
	if stamp := c.stopAt.Load(); stamp > 0 {
		closedAt = time.Duration(stamp - 1)
	}
	m.DurationMS = max(0, closedAt.Milliseconds())
	m.State = "captured"

	if _, err := discoverSegments(tracks, &m); err != nil {
		c.err = fmt.Errorf("inspect meeting originals: %w", err)
		m.State, m.Error = "interrupted", c.err.Error()
	}
	select {
	case <-c.ready:
	default:
		c.err = errors.Join(c.err, errors.New("no meeting source produced audio"))
		m.State, m.Error = "failed", c.err.Error()
	}
	if ctx.Err() != nil && c.stopAt.Load() == 0 && m.State == "captured" {
		m.State = "interrupted"
	}
	m.EndedUTC = c.started.Add(time.Duration(m.DurationMS) * time.Millisecond).UTC().Format(time.RFC3339Nano)
	captureJob := Job{State: "ready"}
	for _, track := range m.Tracks {
		if track.Error != "" {
			captureJob.State = "partial"
			break
		}
	}
	if m.State != "captured" {
		captureJob = Job{State: m.State, Error: m.Error}
	}
	m.Jobs = map[string]Job{"capture": captureJob}
	c.err = errors.Join(c.err, syncDir(tracks), writeManifest(root, &m))
}

func (c *Capture) captureTrack(ctx context.Context, tracks *os.Root, index int, s sessiontypes.Source, open Opener) string {
	backoff := initialBackoff
	var history string
	var seq int
	for epoch := 0; ctx.Err() == nil; epoch++ {
		attempt, cancel := context.WithCancel(ctx)
		timeout := time.AfterFunc(sourceTimeout, cancel)
		stream, err := open(attempt, s)
		if err == nil && (stream.PCM == nil || !validPCM(stream.Rate, stream.Channels)) {
			err = errors.New("unsupported capture format")
		}
		var closeSource func() bool
		var closeOnce func()
		if stream.PCM != nil {
			closeOnce = sync.OnceFunc(func() { _ = stream.PCM.Close() })
			// Cancellation can close the source independently of a slow disk.
			closeSource = context.AfterFunc(attempt, closeOnce)
		}
		var diskErr error
		var data bool
		if err == nil {
			data, err, diskErr = c.consume(attempt, tracks, index, s.ID, epoch, &seq, stream, timeout)
		}
		timeout.Stop()
		cancel()
		if closeSource != nil {
			closeSource()
			closeOnce()
		}
		if diskErr != nil {
			msg := fmt.Sprintf("%s: source storage failed: %v", s.ID, diskErr)
			c.warning(index, msg)
			// Fail this source, leaving others and their already saved audio intact.
			// Wait for the owner so stopping remains available even when every disk
			// writer fails after readiness.
			<-ctx.Done()
			return appendError(history, msg)
		}
		if ctx.Err() != nil {
			break
		}
		if err == nil {
			err = io.EOF
		}
		msg := fmt.Sprintf("%s: source unavailable: %v", s.ID, err)
		c.warning(index, msg)
		if history == "" {
			history = msg
		} // bounded across repeated identical failures
		if data {
			backoff = initialBackoff
		}
		select {
		case <-ctx.Done():
			return history
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 3*time.Second)
	}
	return history
}

func (c *Capture) consume(ctx context.Context, tracks *os.Root, index int, id string, epoch int, seq *int, s Stream, timeout *time.Timer) (data bool, sourceErr, diskErr error) {
	bytesPerFrame := s.Channels * 2
	capacity := s.Rate * bytesPerFrame * int(segmentDuration/time.Second)
	block := make([]byte, max(s.Rate/50, 1)*bytesPerFrame)
	var f *os.File
	var size int
	var start int64
	var clock segmentClock
	closeSegment := func() error {
		if f == nil {
			return nil
		}
		_, err := f.WriteAt(header(s.Rate, s.Channels, uint32(size)), 0)
		err = errors.Join(err, finishFile(f))
		f = nil
		return err
	}
	defer func() { diskErr = errors.Join(diskErr, closeSegment()) }()
	for ctx.Err() == nil {
		n, readErr := io.ReadFull(s.PCM, block)
		timeout.Reset(sourceTimeout)
		if n > 0 {
			n -= n % bytesPerFrame
			var observed int64
			if f == nil || clock.due(size, s.Rate*bytesPerFrame) {
				observed = time.Since(c.started).Milliseconds() - int64(n/bytesPerFrame)*1000/int64(s.Rate)
				if f != nil {
					clock.observe(observed + int64((capacity-size)/bytesPerFrame)*1000/int64(s.Rate))
				}
			}
			var consumed int
			for consumed < n {
				if f == nil || size == capacity {
					nominal := int64(-1)
					if f != nil {
						nominal = start + segmentDuration.Milliseconds()
						if err := closeSegment(); err != nil {
							return data, nil, err
						}
					}
					anchor := max(0, observed)
					if nominal >= 0 {
						anchor = clock.boundary(nominal)
					}
					start, size = anchor, 0
					clock = segmentClock{}
					name := fmt.Sprintf("%s-%06d-%06d-%d-%d-%d.wav", id, epoch, *seq, start, s.Rate, s.Channels)
					if _, _, _, _, _, _, ok := segmentName("tracks/" + name); !ok {
						return data, nil, errors.New("meeting exceeds segment format limits")
					}
					var err error
					f, err = tracks.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
					if err != nil {
						return data, nil, err
					}
					(*seq)++
					if _, err := f.Write(header(s.Rate, s.Channels, uint32(capacity))); err != nil {
						return data, nil, err
					}
					if err := syncDir(tracks); err != nil {
						return data, nil, err
					}
				}
				chunk := block[consumed:min(n, consumed+capacity-size)]
				position := start + int64(size/bytesPerFrame)*1000/int64(s.Rate)
				written, err := f.Write(chunk)
				size += written
				if err != nil {
					return data, nil, err
				}
				consumed += written
				if !data {
					c.warning(index, "")
					c.first.Do(func() { close(c.ready) })
					data = true
				}
				if len(c.preview) < cap(c.preview) {
					pcm := sessiontypes.PCM{Track: id, StartMS: position, Rate: s.Rate, Channels: s.Channels, S16LE: append([]byte(nil), chunk...)}
					select {
					case c.preview <- pcm:
					default:
					}
				}
			}
		}
		if readErr != nil {
			return data, readErr, nil
		}
	}
	return data, ctx.Err(), nil
}

// segmentClock takes five predictions in the final half-second of a full
// span. Each projects the current sample position to the boundary. The
// median rejects transient scheduling jitter, and all other PCM is placed by
// sample index, never by individual read/packet arrival times.
type segmentClock struct {
	predictions [5]int64
	n           int
}

func (c *segmentClock) due(bytes, bytesPerSecond int) bool {
	return c.n < len(c.predictions) && int64(bytes)*1000/int64(bytesPerSecond) >= segmentDuration.Milliseconds()-500+int64(c.n)*100
}
func (c *segmentClock) observe(boundary int64) {
	c.predictions[c.n] = boundary
	c.n++
}
func (c *segmentClock) boundary(nominal int64) int64 {
	if c.n == 0 {
		return nominal
	}
	values := c.predictions[:c.n]
	slices.Sort(values)
	observed := values[len(values)/2]
	const tolerance = segmentDuration / time.Millisecond / 200 // reject >5000 ppm
	if observed < nominal-int64(tolerance) || observed > nominal+int64(tolerance) {
		return nominal
	}
	return observed
}
