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
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/desktopmac"
	"github.com/joegoldin/audiomemo/internal/record"
	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/joegoldin/audiomemo/internal/stream"
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
	sides                           *transcribe.SidePreview
	uiDone, uiStop                  chan struct{}
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
func Start(ctx context.Context, c *config.Config, id, device string, both bool, caption func(sessiontypes.Preview), finish func(string)) (sessiontypes.Quick, error) {
	if _, err := exec.LookPath(c.Transcribe.Nemo.Binary); err != nil {
		return nil, fmt.Errorf("Quick Scribe needs local NeMo: %w", err)
	}
	ds, err := record.ListDevices()
	if err != nil {
		return nil, err
	}
	devices, roles, err := quickDevices(c, device, both, ds)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "darwin" && slices.ContainsFunc(devices, func(device string) bool { return device != record.SystemAudioDevice }) {
		if err := desktopmac.CheckMicrophonePermission(ctx); err != nil {
			return nil, err
		}
	}
	r, err := record.Start(record.RecordOpts{Devices: devices, Roles: roles,
		Format: "wav", SampleRate: 16000, Channels: 1, OutputPath: os.DevNull, LivePCM: true, MaxDuration: 5 * time.Minute})
	if err != nil {
		return nil, desktopmac.DescribeSystemAudioError(err)
	}
	s := newSession(ctx, r.PCMReader, r.CloseCapture, func(ctx context.Context, b []byte) (string, error) {
		nemo := transcribe.NewNemo(c.Transcribe.Nemo)
		if len(roles) > 0 {
			return nemo.TranscribeSides(ctx, b, c.Transcribe.Language)
		}
		res, e := nemo.TranscribePCM(ctx, b, 16000, transcribe.TranscribeOpts{Language: c.Transcribe.Language})
		if e != nil {
			return "", e
		}
		return res.Text, nil
	}, finish, sessiontypes.QuickPCMBytes)
	if len(roles) > 0 {
		s.sides, err = transcribe.StartSidePreview(s.ctx, c, "nemo", r.PCMReader, true)
	} else {
		s.preview, err = transcribe.NewLiveStreamer(c, "nemo")
		if err == nil {
			err = s.preview.StartMemory(s.ctx, s.feed)
		}
	}
	if err != nil {
		_ = s.source.Close()
		s.Cancel()
		return nil, err
	}
	s.uiDone, s.uiStop = make(chan struct{}), make(chan struct{})
	go s.captions(id, r.Level, caption)

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

// Implicit single-device dictation stays microphone-only. Explicit device
// and group selections may include system audio.
func quickDevices(c *config.Config, selected string, both bool, ds []record.Device) ([]string, []string, error) {
	if both {
		selected = c.Desktop.DictationGroup
		if selected == "" {
			selected = c.Desktop.MeetingDevice
		}
		if _, ok := c.DeviceGroups[selected]; !ok {
			return nil, nil, fmt.Errorf("set desktop.dictation_group to a device group with Us and Them inputs")
		}
	}
	if selected == "" {
		name, err := mic(c, selected, ds)
		return []string{name}, nil, err
	}
	sources, err := record.ResolveSources(c, selected, ds)
	if err != nil {
		return nil, nil, err
	}
	devices := make([]string, 0, len(sources))
	for _, source := range sources {
		devices = append(devices, source.Device)
	}
	roles, err := record.RecordingRoles(c, devices, ds)
	if err == nil && both && len(roles) == 0 {
		return nil, nil, fmt.Errorf("device group %q needs both Us and Them inputs", selected)
	}
	return devices, roles, err
}

func (s *Session) captions(id string, levels <-chan float64, caption func(sessiontypes.Preview)) {
	defer close(s.uiDone)
	tick := time.NewTicker(64 * time.Millisecond)
	defer tick.Stop()
	var level float64
	var lastAudio time.Time
	var last sessiontypes.Preview
	for {
		select {
		case <-s.uiStop:
			return
		case reading, ok := <-levels:
			if !ok {
				levels = nil
			} else {
				level = max(level, stream.NormalizeLevel(reading))
				lastAudio = time.Now()
			}
		case <-tick.C:
			p := sessiontypes.Preview{Session: id}
			if s.sides != nil {
				p = s.sides.Snapshot()
				p.Session = id
			} else {
				if time.Since(lastAudio) > 200*time.Millisecond {
					level = 0
				}
				lane := s.preview.Caption(level)
				p.Stable, p.Tentative, p.Level = lane.Stable, lane.Tentative, lane.Level
				level *= 0.75
			}
			if p != last {
				caption(p)
				last = p
			}
		}
	}
}

func (s *Session) CloseCapture(ctx context.Context) error {
	s.stopOnce.Do(func() {
		deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		s.closeErr = s.closeSource(deadline)
		if s.closeErr != nil {
			_ = s.source.Close()
		}
		if s.uiDone != nil {
			close(s.uiStop)
			<-s.uiDone
		}
		if s.sides != nil {
			s.sides.Stop()
		} else if s.preview != nil {
			s.preview.Stop()
		}
		_ = s.source.Close()
	})
	return s.closeErr
}
func (s *Session) Cancel() {
	s.cancel()
	_ = s.source.Close()
	_ = s.CloseCapture(context.Background())
}
func (s *Session) Stop(ctx context.Context) (string, error) {
	s.finalOnce.Do(func() {
		err := s.CloseCapture(ctx)
		defer s.cancel()
		if e := s.ctx.Err(); e != nil {
			s.finalErr = e
			return
		}
		b := s.pcm.Bytes()
		if s.sides != nil {
			var readErr error
			b, readErr = s.sides.PCM()
			if readErr != nil {
				s.finalErr = readErr
				return
			}
		}
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
		if s.finalErr != nil {
			// Preview packets can be dropped, so a draft cannot replace the final.
			s.text = ""
			return
		}
		if strings.TrimSpace(s.text) == "" {
			s.finalErr = errors.Join(errors.New("no speech captured"), err)
		}
	})
	return s.text, s.finalErr
}
