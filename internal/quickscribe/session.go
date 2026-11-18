// Package quickscribe transcribes a short microphone clip without creating audio
// or text files. Preview is speculative; the final model hears the whole clip.
package quickscribe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/joegoldin/audiomemo/internal/transcribe"
)

func mic(c *config.Config, selected string, ds []record.Device) (string, error) {
	if selected == "" {
		selected = c.Desktop.DictationMic
	}
	if selected == "" {
		if _, group := c.DeviceGroups[c.Record.Device]; !group {
			selected = c.Record.Device
		}
	}
	var eligible []string
	for _, d := range ds {
		if d.IsMonitor || d.Name == record.SystemAudioDevice {
			continue
		}
		if selected != "" {
			sources, err := record.ResolveSources(c, selected, ds)
			if err != nil {
				return "", err
			}
			if _, group := c.DeviceGroups[selected]; group || len(sources) != 1 {
				return "", fmt.Errorf("dictation needs one microphone, not a group")
			}
			if sources[0].Device == d.Name {
				return d.Name, nil
			}
			continue
		}
		role, _, err := record.SourceRole(c, d, ds)
		if err != nil {
			return "", err
		}
		if role == "us" {
			eligible = append(eligible, d.Name)
		}
	}
	if len(eligible) == 1 {
		return eligible[0], nil
	}
	return "", fmt.Errorf("select one microphone with desktop.dictation_mic or dictate start -D")
}

type finalFunc func(context.Context, []byte) (string, error)
type Session struct {
	ctx                             context.Context
	cancel                          context.CancelFunc
	source                          io.ReadCloser
	feed                            io.Reader
	closeSource                     func(context.Context) error
	finish                          func(string)
	final                           finalFunc
	limit                           int
	pcm                             bytes.Buffer // written by the single preview pump before capture closes
	preview                         *transcribe.Streamer
	uiDone                          chan struct{}
	stopOnce, finalOnce, notifyOnce sync.Once
	closeErr, finalErr              error
	text                            string
}

func newSession(ctx context.Context, r io.ReadCloser, closeSource func(context.Context) error, final finalFunc, finish func(string), limit int) *Session {
	life, cancel := context.WithCancel(ctx)
	s := &Session{ctx: life, cancel: cancel, source: r, closeSource: closeSource, final: final, finish: finish, limit: limit}
	s.feed = io.TeeReader(io.LimitReader(r, int64(limit)), s)
	return s
}
func (s *Session) Write(p []byte) (int, error) {
	n, e := s.pcm.Write(p)
	if s.pcm.Len() == s.limit {
		s.notify("five-minute dictation limit reached")
	}
	return n, e
}
func (s *Session) notify(reason string) {
	s.notifyOnce.Do(func() {
		if s.finish != nil {
			go s.finish(reason)
		}
	})
}
func Start(ctx context.Context, c *config.Config, id, device string, caption func(sessiontypes.Preview), finish func(string)) (sessiontypes.Quick, error) {
	if _, err := exec.LookPath(c.Transcribe.Nemo.Binary); err != nil {
		return nil, fmt.Errorf("Quick Scribe needs local NeMo: %w", err)
	}
	ds, err := record.ListDevices()
	if err != nil {
		return nil, err
	}
	name, err := mic(c, device, ds)
	if err != nil {
		return nil, err
	}
	view, err := transcribe.NewLiveStreamer(c, "nemo")
	if err != nil {
		return nil, err
	}
	r, err := record.Start(record.RecordOpts{Device: name, Format: "wav", SampleRate: 16000, Channels: 1, OutputPath: os.DevNull, LivePCM: true, MaxDuration: 5 * time.Minute})
	if err != nil {
		return nil, err
	}
	s := newSession(ctx, r.PCMReader, r.CloseCapture, func(ctx context.Context, b []byte) (string, error) {
		res, e := transcribe.NewNemo(c.Transcribe.Nemo).TranscribePCM(ctx, b, 16000, transcribe.TranscribeOpts{Language: c.Transcribe.Language})
		if e != nil {
			return "", e
		}
		return res.Text, nil
	}, finish, sessiontypes.QuickPCMBytes)
	s.preview = view
	s.uiDone = make(chan struct{})
	if err = view.StartMemory(s.ctx, s.feed); err != nil {
		s.preview = nil
		s.Cancel()
		return nil, err
	}
	go func() {
		defer close(s.uiDone)
		for {
			select {
			case <-s.ctx.Done():
				return
			case p, ok := <-view.Partial:
				if !ok {
					return
				}
				caption(sessiontypes.Preview{Session: id, Stable: view.FullText(), Tentative: p})
			case _, ok := <-view.Committed:
				if !ok {
					return
				}
				caption(sessiontypes.Preview{Session: id, Stable: view.FullText()})
			}
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			s.Cancel()
		case err := <-r.Done:
			if err != nil {
				s.notify("microphone disconnected")
			} else {
				s.notify("microphone stopped")
			}
		}
	}()
	return s, nil
}
func (s *Session) CloseCapture(ctx context.Context) error {
	s.stopOnce.Do(func() {
		deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		s.closeErr = s.closeSource(deadline)
		if s.closeErr != nil {
			_ = s.source.Close()
		}
		if s.preview != nil {
			s.preview.Stop()
			<-s.uiDone
		}
		_ = s.source.Close()
	})
	return s.closeErr
}
func (s *Session) Cancel() { s.cancel(); _ = s.CloseCapture(context.Background()) }
func (s *Session) Stop(ctx context.Context) (string, error) {
	s.finalOnce.Do(func() {
		err := s.CloseCapture(ctx)
		defer s.cancel()
		if e := s.ctx.Err(); e != nil {
			s.finalErr = e
			return
		}
		b := s.pcm.Bytes()
		if len(b) < 2 || bytes.Count(b, []byte{0}) == len(b) {
			s.finalErr = errors.New("no speech captured")
			return
		}
		life, cancel := context.WithTimeout(ctx, 2*time.Minute)
		stop := context.AfterFunc(s.ctx, cancel)
		defer stop()
		defer cancel()
		s.text, s.finalErr = s.final(life, b[:len(b)&^1])
		clear(b)
		s.pcm.Reset()
		if s.finalErr != nil && life.Err() == nil && s.preview != nil {
			s.text = s.preview.FullText()
			if s.text != "" {
				s.finalErr = nil
			}
		}
		if s.finalErr == nil && strings.TrimSpace(s.text) == "" {
			s.finalErr = errors.Join(errors.New("no speech captured"), err)
		}
	})
	return s.text, s.finalErr
}
