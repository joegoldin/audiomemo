//go:build linux

// Package desktoplinux bridges the daemon to the optional Plasma/Wayland helper.
package desktoplinux

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
)

// ErrPasteUnavailable reports a successful clipboard copy, not a failed delivery.
var ErrPasteUnavailable = errors.New("copied to clipboard; safe automatic paste is unavailable on Wayland")

var (
	errUnavailable = errors.New("Linux desktop helper unavailable; use the CLI")
	errClipboard   = errors.New("Wayland clipboard unavailable; use Copy last result once the desktop is available")
	errLocked      = errors.New("clipboard not changed while the session is locked or lock state is unknown")
)

const helperName = "audiomemo-desktop-linux"
const helperTimeout = 4 * time.Second

// Adapter has no graphical dependencies in the daemon process.
type Adapter struct {
	command func(context.Context) (*exec.Cmd, error)
	mu      sync.Mutex
	status  sessiontypes.Status
	preview sessiontypes.Preview
	active  *bridge
}

var _ sessiontypes.Desktop = (*Adapter)(nil)

func New() *Adapter {
	return &Adapter{command: helperCommand}
}

func helperCommand(ctx context.Context) (*exec.Cmd, error) {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return nil, errUnavailable
	}
	path := os.Getenv("AUDIOMEMO_DESKTOP_LINUX_HELPER")
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, errUnavailable
		}
		path = filepath.Join(filepath.Dir(exe), "..", "libexec", helperName)
	}
	if !filepath.IsAbs(path) {
		return nil, errUnavailable
	}
	cmd := exec.CommandContext(ctx, path)
	// Only the fixed protocol is consumed; toolkit diagnostics need not be logged.
	return cmd, nil
}

// Target intentionally returns no identity. An active output is not a focused
// application and the RemoteDesktop portal does not fence injected input to one.
func (*Adapter) Target() string { return "" }

func (d *Adapter) Update(st sessiontypes.Status) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if st.Session != d.status.Session || !captionsActive(st.Capture) {
		d.preview = sessiontypes.Preview{}
	}
	d.status = st
	d.wake()
}

func (d *Adapter) Caption(p sessiontypes.Preview) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p.Session == "" || d.status.Session != p.Session || !captionsActive(d.status.Capture) {
		return
	}
	p.Stable = tail(p.Stable)
	p.Tentative = tail(p.Tentative)
	d.preview = p
	d.wake()
}

func captionsActive(capture string) bool { return capture == "dictating" || capture == "meeting" }

func tail(s string) string {
	if len(s) <= 4096 {
		return s
	}
	i := len(s) - 4096
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return "…" + strings.Clone(s[i:])
}

// wake coalesces views, never clipboard requests. Caller holds d.mu.
func (d *Adapter) wake() {
	if d.active != nil {
		select {
		case d.active.views <- struct{}{}:
		default:
		}
	}
}

type viewStatus struct {
	Capture string `json:"capture"`
	Session string `json:"session"`
	Started string `json:"started_utc"`
	Job     string `json:"job_state"`
	Warning bool   `json:"warning"`
}

type copyCall struct {
	ctx  context.Context
	text string
	ack  chan error
}

type helperEvent struct {
	Type   string `json:"type"`
	Action string `json:"action"`
	ID     uint64 `json:"id"`
	Code   string `json:"code"`
}

type bridge struct {
	ctx    context.Context
	cancel context.CancelFunc
	ready  chan struct{}
	views  chan struct{}
	copies chan copyCall
	acks   chan helperEvent
	menu   chan string
	notes  chan string
}

func (d *Adapter) Deliver(ctx context.Context, text, _ string, paste bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if text == "" {
		return nil
	}
	// Bound work before encoding; the writer also checks the actual JSON size.
	if len(text) >= sessiontypes.MaxFrame {
		return errors.New("result exceeds desktop clipboard frame limit")
	}
	d.mu.Lock()
	b := d.active
	d.mu.Unlock()
	if b == nil {
		return errUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()
	call := copyCall{ctx: ctx, text: text, ack: make(chan error, 1)}
	select {
	case <-b.ready:
	case <-ctx.Done():
		return ctx.Err()
	case <-b.ctx.Done():
		return errUnavailable
	}
	select {
	case b.copies <- call:
	case <-ctx.Done():
		return ctx.Err()
	case <-b.ctx.Done():
		return errUnavailable
	}
	select {
	case err := <-call.ack:
		if err == nil && paste {
			return ErrPasteUnavailable
		}
		return err
	case <-b.ctx.Done():
		return errUnavailable
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Adapter) Run(ctx context.Context, controller sessiontypes.Controller) error {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	b := &bridge{
		ctx: childCtx, cancel: cancel, ready: make(chan struct{}), views: make(chan struct{}, 1),
		copies: make(chan copyCall), acks: make(chan helperEvent, 1), menu: make(chan string, 1), notes: make(chan string, 1),
	}
	d.mu.Lock()
	if d.active != nil {
		d.mu.Unlock()
		return errors.New("Linux desktop helper already running")
	}
	d.active = b
	d.wake()
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.active = nil
		d.preview = sessiontypes.Preview{}
		d.mu.Unlock()
	}()

	cmd, err := d.command(childCtx)
	if err != nil {
		return runError(ctx)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return runError(ctx)
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return runError(ctx)
	}
	defer out.Close()
	if err := cmd.Start(); err != nil {
		return runError(ctx)
	}

	var workers sync.WaitGroup
	workers.Go(func() { defer cancel(); d.write(b, in) })
	workers.Go(func() { b.control(controller) })
	startup := time.AfterFunc(helperTimeout, cancel)
	b.read(out, startup)
	startup.Stop()
	cancel()
	workers.Wait()
	_ = cmd.Wait()
	return runError(ctx)
}

func runError(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	return errUnavailable
}

func (b *bridge) read(r io.Reader, startup *time.Timer) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 4096)
	ready := false
	for s.Scan() {
		var e helperEvent
		if json.Unmarshal(s.Bytes(), &e) != nil {
			return
		}
		if !ready {
			if e.Type != "ready" {
				return
			}
			ready = true
			startup.Stop()
			close(b.ready)
			continue
		}
		switch e.Type {
		case "action":
			switch e.Action {
			case "status", "dictate.start", "dictate.stop", "meeting.start", "meeting.stop", "last.copy", "quit":
				select {
				case b.menu <- e.Action:
				default:
					b.note("AudioMemo is handling another menu action")
				}
			}
		case "copied":
			select {
			case b.acks <- e:
			default:
				return
			}
		case "capability":
			switch e.Code {
			case "no-preview":
				fmt.Fprintln(os.Stderr, "AudioMemo: Wayland layer-shell unavailable; captions disabled")
			case "no-tray":
				fmt.Fprintln(os.Stderr, "AudioMemo: Plasma recording indicator unavailable; use the CLI")
			case "no-lock-state":
				fmt.Fprintln(os.Stderr, "AudioMemo: screen lock status unavailable; desktop actions and clipboard disabled")
			case "no-clipboard":
				fmt.Fprintln(os.Stderr, "AudioMemo: Wayland data-control unavailable; clipboard delivery disabled")
			}
		}
	}
}

func (b *bridge) note(text string) {
	select {
	case b.notes <- text:
	default:
	}
}

func (b *bridge) control(controller sessiontypes.Controller) {
	for {
		select {
		case <-b.ctx.Done():
			return
		case action := <-b.menu:
			if b.ctx.Err() != nil {
				return
			}
			reply := controller.Do(b.ctx, sessiontypes.Request{Version: sessiontypes.Version, Action: action})
			// Controller diagnostics can include paths or content. Keep UI feedback fixed.
			switch {
			case action == "last.copy" && reply.Error != "":
				b.note("Could not copy the last result; check AudioMemo status")
			case action == "last.copy":
				b.note("Copied to the clipboard")
			case reply.Error != "":
				b.note("AudioMemo could not complete the menu action; check status")
			}
		}
	}
}

func (d *Adapter) write(b *bridge, w io.Writer) {
	select {
	case <-b.ready:
	case <-b.ctx.Done():
		return
	}
	var id uint64
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.views:
			d.mu.Lock()
			s, p := d.status, d.preview
			d.mu.Unlock()
			if !b.send(w, struct {
				Type    string               `json:"type"`
				Status  viewStatus           `json:"status"`
				Preview sessiontypes.Preview `json:"preview"`
			}{"view", viewStatus{s.Capture, s.Session, s.StartedUTC, s.JobState, s.Warning != ""}, p}) {
				return
			}
		case note := <-b.notes:
			if !b.send(w, map[string]string{"type": "note", "text": note}) {
				return
			}
		case call := <-b.copies:
			id++
			if !b.copy(w, id, call) {
				return
			}
		}
	}
}

func (b *bridge) send(w io.Writer, v any) bool {
	timer := time.AfterFunc(helperTimeout, b.cancel)
	defer timer.Stop()
	return json.NewEncoder(w).Encode(v) == nil
}

func (b *bridge) copy(w io.Writer, id uint64, call copyCall) bool {
	deadline, _ := call.ctx.Deadline()
	frame, err := json.Marshal(struct {
		Type     string `json:"type"`
		ID       uint64 `json:"id"`
		Text     string `json:"text"`
		Deadline int64  `json:"deadline_ms"`
	}{"copy", id, call.text, deadline.UnixMilli()})
	if err != nil || len(frame) >= sessiontypes.MaxFrame {
		call.ack <- errors.New("result exceeds desktop clipboard frame limit")
		return true
	}
	if err := call.ctx.Err(); err != nil {
		call.ack <- err
		return true
	}
	// A blocked or uncertain delivery must not keep the adapter wedged or be
	// retried automatically. Killing the helper also releases any selection it owns.
	timer := time.AfterFunc(helperTimeout, b.cancel)
	defer timer.Stop()
	if _, err := w.Write(append(frame, '\n')); err != nil {
		return false
	}
	select {
	case e := <-b.acks:
		if e.ID != id {
			return false
		}
		switch e.Code {
		case "":
			call.ack <- nil
		case "locked":
			call.ack <- errLocked
		default:
			call.ack <- errClipboard
		}
		return true
	case <-b.ctx.Done():
		return false
	}
}
