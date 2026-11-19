package transcribe

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/joegoldin/audiomemo/internal/stream"
)

// SidePreview runs separate recognition sessions over the aligned capture
// channels. The Them channel has already been mixed by the recorder.
type SidePreview struct {
	Views     [2]*Streamer
	input     io.ReadCloser
	pcm       bytes.Buffer
	done      chan struct{}
	mu        sync.Mutex
	level     [2]float64
	lastAudio time.Time
	err       error
	once      sync.Once
}

func StartSidePreview(ctx context.Context, cfg *config.Config, backend string, input io.ReadCloser, retain bool) (*SidePreview, error) {
	p := &SidePreview{input: input, done: make(chan struct{})}
	var outputs [2]io.Writer
	var pipes [2]*io.PipeWriter
	var readers [2]*io.PipeReader
	for i := range p.Views {
		s, err := NewLiveStreamer(cfg, backend)
		if err != nil {
			return nil, err
		}
		if s == nil {
			return nil, fmt.Errorf("%s has no live transcription backend", backend)
		}
		p.Views[i] = s
	}
	for i, s := range p.Views {
		r, w := io.Pipe()
		pipes[i], readers[i] = w, r
		outputs[i] = io.MultiWriter(sideMeter{p, i}, w)
		// Managed streams have no synchronous network startup. Only the CLI
		// supplies auto here, after the user explicitly enables cloud fallback.
		if err := s.start(ctx, r, io.Discard); err != nil {
			for j := 0; j <= i; j++ {
				pipes[j].Close()
				p.Views[j].Stop()
			}
			return nil, err
		}
	}
	interrupt := context.AfterFunc(ctx, func() {
		for _, r := range readers {
			r.Close()
		}
	})
	go func() {
		defer interrupt()
		defer close(p.done)
		defer pipes[0].Close()
		defer pipes[1].Close()
		var save io.Writer
		if retain {
			save = &p.pcm
		}
		p.err = feedSides(input, outputs, save)
	}()
	return p, nil
}

// Stop is called after capture stops (or its reader closes). Both recognition
// sessions flush in parallel; no model remains loaded for the final pass.
func (p *SidePreview) Stop() {
	p.once.Do(func() {
		<-p.done
		p.input.Close()
		var wg sync.WaitGroup
		for _, s := range p.Views {
			wg.Go(s.Stop)
		}
		wg.Wait()
	})
}

func (p *SidePreview) PCM() ([]byte, error) {
	<-p.done
	return p.pcm.Bytes(), p.err
}

func (p *SidePreview) Snapshot() st.Preview {
	p.mu.Lock()
	levels := p.level
	if time.Since(p.lastAudio) > 250*time.Millisecond {
		levels = [2]float64{}
	}
	p.mu.Unlock()
	return st.Preview{Split: true, Us: p.Views[0].Caption(levels[0]), Them: p.Views[1].Caption(levels[1])}
}

func (s *Streamer) Caption(level float64) st.CaptionLane {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Bound the hot UI path, independently of the full saved transcript.
	var stable string
	for i := len(s.committed) - 1; i >= 0 && len(stable) < 2048; i-- {
		if stable == "" {
			stable = s.committed[i]
		} else {
			stable = s.committed[i] + " " + stable
		}
	}
	return st.CaptionLane{Stable: stable, Tentative: s.draft, Level: level}
}

type sideMeter struct {
	p    *SidePreview
	side int
}

func (m sideMeter) Write(p []byte) (int, error) {
	var square float64
	for i := 0; i < len(p); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(p[i:]))) / 32768
		square += v * v
	}
	level := stream.NormalizeLevel(10 * math.Log10(square/float64(len(p)/2)))
	m.p.mu.Lock()
	m.p.level[m.side], m.p.lastAudio = level, time.Now()
	m.p.mu.Unlock()
	return len(p), nil
}

func feedSides(src io.Reader, dest [2]io.Writer, retain io.Writer) error {
	var data [4096]byte
	var mono [2][2048]byte
	for {
		n, err := io.ReadFull(src, data[:])
		if n%4 != 0 {
			return fmt.Errorf("incomplete Us/Them PCM frame")
		}
		if n > 0 {
			if retain != nil {
				if _, e := retain.Write(data[:n]); e != nil {
					return e
				}
			}
			for i := 0; i < n; i += 4 {
				copy(mono[0][i/2:], data[i:i+2])
				copy(mono[1][i/2:], data[i+2:i+4])
			}
			for i, w := range dest {
				if w != nil {
					if _, e := w.Write(mono[i][:n/2]); e != nil {
						dest[i] = nil
					}
				}
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
