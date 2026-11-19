//go:build darwin && cgo

// Package desktopmac implements the in-process macOS desktop for AudioMemo.
package desktopmac

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices -framework AVFoundation -framework Carbon -framework Security
#include <stdlib.h>
#include "desktop_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/cgo"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
)

// New returns the desktop adapter. Call Run on the initial OS thread.
func New() st.Desktop { return &desktop{} }

type desktop struct {
	mu    sync.Mutex
	state viewState
	run   *uiRun
}

var _ st.Desktop = (*desktop)(nil)

type uiRun struct {
	desktop *desktop
	ctx     context.Context
	cancel  context.CancelFunc
	ctl     st.Controller
	calls   chan func(unsafe.Pointer)
	actions chan st.Request
	quit    sync.Once
	busy    atomic.Bool
	gesture atomic.Bool

	// Only touched by AppKit callbacks on the main thread.
	menu    menuState
	preview st.Preview
	phase   string
}

var appMu sync.Mutex // NSApplication is process-wide.

func (d *desktop) Run(ctx context.Context, controller st.Controller) error {
	if err := ctx.Err(); err != nil {
		return nil
	}
	if !appMu.TryLock() {
		return errors.New("AudioMemo's macOS desktop is already running")
	}
	defer appMu.Unlock()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	u := &uiRun{desktop: d, ctx: ctx, cancel: cancel, ctl: controller,
		calls: make(chan func(unsafe.Pointer), 8), actions: make(chan st.Request, 1)}
	d.mu.Lock()
	d.run = u
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.run = nil
		d.state = viewState{}
		d.mu.Unlock()
	}()
	go u.handleActions()

	h := cgo.NewHandle(u)
	defer h.Delete()
	switch C.am_ui_run(C.uintptr_t(h)) {
	case C.AMUIStopped:
		return nil
	case C.AMNotMainThread:
		return errors.New("run the AudioMemo desktop on the initial macOS thread; lock it before starting goroutines")
	case C.AMNoAquaSession:
		return ErrUnavailable
	default:
		return errors.New("could not create the AudioMemo menu bar item; desktop controls were not started")
	}
}

func (d *desktop) Update(status st.Status) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state.update(status)
}

func (d *desktop) Caption(p st.Preview) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state.caption(p)
}

func (d *desktop) ui() *uiRun {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.run
}

const uiWait = 2 * time.Second

func uiCall[T any](ctx context.Context, u *uiRun, f func(context.Context, unsafe.Pointer) T) (T, error) {
	var zero T
	if u == nil {
		return zero, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, uiWait)
	defer cancel()
	result := make(chan T, 1)
	call := func(ui unsafe.Pointer) {
		if ctx.Err() == nil && u.ctx.Err() == nil {
			result <- f(ctx, ui)
		}
	}
	select {
	case u.calls <- call:
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-u.ctx.Done():
		return zero, ErrUnavailable
	}
	select {
	case answer := <-result:
		return answer, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-u.ctx.Done():
		return zero, ErrUnavailable
	}
}

func (d *desktop) Target() string {
	u := d.ui()
	id, err := uiCall(context.Background(), u, func(_ context.Context, ui unsafe.Pointer) uint64 {
		return uint64(C.am_ui_target(ui, boolInt(u.gesture.Load())))
	})
	if err != nil || id == 0 {
		return ""
	}
	return strconv.FormatUint(id, 10)
}

func (d *desktop) Deliver(ctx context.Context, text, target string, paste bool) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	id, _ := strconv.ParseUint(target, 10, 64)
	u := d.ui()
	result, err := uiCall(ctx, u, func(ctx context.Context, ui unsafe.Pointer) deliveryResult {
		valid := cgo.NewHandle(func() bool { return ctx.Err() == nil && u.ctx.Err() == nil })
		defer valid.Delete()
		return deliveryResult(C.am_ui_deliver(ui, unsafe.Pointer(unsafe.StringData(text)), C.size_t(len(text)),
			C.uint64_t(id), boolInt(paste), C.uintptr_t(valid)))
	})
	if err != nil {
		return err
	}
	_, err = result.outcome()
	return err
}

//export amGoCanDeliver
func amGoCanDeliver(handle C.uintptr_t) C.int {
	return boolInt(cgo.Handle(handle).Value().(func() bool)())
}

func checkTargetFence(authorized, supplied, found, changed, editable, matches bool) deliveryResult {
	return deliveryResult(C.am_target_fence(boolInt(authorized), boolInt(supplied), boolInt(found),
		boolInt(changed), boolInt(editable), boolInt(matches)))
}

func boolInt(v bool) C.int {
	if v {
		return 1
	}
	return 0
}

func shortText(s string) string {
	const tail = 2048
	if len(s) > tail {
		start := len(s) - tail
		for s[start]&0xc0 == 0x80 {
			start++
		}
		s = s[start:]
	}
	return strings.ReplaceAll(s, "\x00", "\ufffd")
}

func renderMenu(ui unsafe.Pointer, m menuState) {
	title, status, warning := C.CString(m.title), C.CString(m.status), C.CString(m.warning)
	defer C.free(unsafe.Pointer(title))
	defer C.free(unsafe.Pointer(status))
	defer C.free(unsafe.Pointer(warning))
	action := func(name string) C.int {
		switch name {
		case "dictate.start", "meeting.start":
			return 1
		case "dictate.stop", "meeting.stop":
			return 2
		default:
			return 0
		}
	}
	icon := C.int(C.AMTrayIdle)
	switch m.icon {
	case trayRecording:
		icon = C.AMTrayRecording
	case trayRefining:
		icon = C.AMTrayRefining
	}
	C.am_ui_render(ui, title, status, warning, action(m.dictate), action(m.meeting), boolInt(m.cancel), icon)
}

func renderCaption(ui unsafe.Pointer, p st.Preview, phase string) {
	texts := []string{p.Session, phase, p.Stable, p.Tentative, p.Us.Stable, p.Us.Tentative, p.Them.Stable, p.Them.Tentative}
	args := make([]*C.char, len(texts))
	for i, text := range texts {
		args[i] = C.CString(shortText(text))
		defer C.free(unsafe.Pointer(args[i]))
	}
	C.am_ui_caption(ui, args[0], args[1], args[2], args[3], C.double(p.Level), boolInt(p.Split),
		args[4], args[5], C.double(p.Us.Level), args[6], args[7], C.double(p.Them.Level))
}

//export amGoTick
func amGoTick(handle C.uintptr_t, ui unsafe.Pointer) {
	u := cgo.Handle(handle).Value().(*uiRun)
	for range cap(u.calls) {
		if u.ctx.Err() != nil {
			C.am_ui_stop()
			return
		}
		select {
		case call := <-u.calls:
			call(ui)
		default:
			goto render
		}
	}
render:
	u.desktop.mu.Lock()
	s := u.desktop.state
	u.desktop.mu.Unlock()
	if menu := s.render(time.Now()); menu != u.menu {
		u.menu = menu
		renderMenu(ui, menu)
	}
	if p, phase := s.overlay(time.Now()); p != u.preview || phase != u.phase {
		u.preview, u.phase = p, phase
		renderCaption(ui, p, phase)
	}
}

//export amGoAction
func amGoAction(handle C.uintptr_t, action C.int, device *C.char) C.int {
	u := cgo.Handle(handle).Value().(*uiRun)
	r := st.Request{Version: st.Version}
	switch action {
	case C.AMQuit:
		u.quit.Do(func() { go u.invoke(st.Request{Version: st.Version, Action: "quit"}) })
		return 1
	case C.AMToggle:
		r.Action = "dictate.toggle"
	case C.AMStartDictate:
		r.Action = "dictate.start"
	case C.AMStopDictate:
		r.Action = "dictate.stop"
	case C.AMCancelDictate:
		r.Action = "dictate.cancel"
	case C.AMStartMeeting:
		r.Action = "meeting.start"
		if device != nil {
			r.Device = strings.TrimSpace(C.GoString(device))
		}
	case C.AMStopMeeting:
		r.Action = "meeting.stop"
	case C.AMCopyLast:
		r.Action = "last.copy"
	default:
		return 0
	}
	if r.Action == "dictate.stop" || r.Action == "dictate.cancel" || r.Action == "meeting.stop" {
		u.desktop.mu.Lock()
		r.Session = u.desktop.state.status.Session
		u.desktop.mu.Unlock()
		if r.Session == "" {
			return 0
		}
	}
	if !u.busy.CompareAndSwap(false, true) {
		u.notice("Another menu action is still running.")
		return 0
	}
	u.gesture.Store(r.Action == "dictate.start" || r.Action == "dictate.toggle")
	u.actions <- r
	return 1
}

func (u *uiRun) notice(message string) {
	u.desktop.mu.Lock()
	defer u.desktop.mu.Unlock()
	if u.desktop.run == u {
		u.desktop.state.notice = message
	}
}

func (u *uiRun) handleActions() {
	for {
		select {
		case <-u.ctx.Done():
			return
		case req := <-u.actions:
			u.invoke(req)
			u.gesture.Store(false)
			u.busy.Store(false)
		}
	}
}

func (u *uiRun) invoke(req st.Request) {
	reply := u.ctl.Do(u.ctx, req)
	if reply.Error != "" {
		message := reply.Status.Warning
		if message == "" {
			message = fmt.Sprintf("AudioMemo could not complete this action (%s).", reply.Error)
		}
		u.notice(message)
	} else if req.Action == "quit" {
		u.cancel()
	} else {
		u.notice("")
	}
}

var micRequestOnce sync.Once
var micPromptFinished = make(chan struct{})

//export amGoMicrophoneResponse
func amGoMicrophoneResponse() { close(micPromptFinished) }

// CheckMicrophonePermission is for an explicit capture start in the app, not a
// background probe. It never prompts after denial or on passive status checks.
// AVFoundation authorizes this process; a separate recorder can still fail.
func CheckMicrophonePermission(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	status := C.am_microphone_status()
	if status == C.AMMicUndetermined {
		micRequestOnce.Do(func() { C.am_microphone_request() })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-micPromptFinished:
			status = C.am_microphone_status()
		}
	}
	switch status {
	case C.AMMicAllowed:
		return nil
	case C.AMMicRestricted:
		return ErrMicrophoneRestricted
	default:
		return ErrMicrophoneDenied
	}
}
