// Package desktopservice owns capture admission, background work and the local
// desktop control socket. Recording and delivery implementations are injected.
package desktopservice

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
)

const (
	maxJobs     = 4
	stopTimeout = 10 * time.Second
)

type Options struct {
	RuntimeDir string
	Desktop    st.Desktop // nil for headless operation
	Paste      bool
	// Producer contexts outlive the admitting request. StartQuick's finish
	// callback is nonblocking and asks the controller to stop this session.
	StartQuick func(ctx context.Context, id, device string, preview func(st.Preview), finish func(reason string)) (st.Quick, error)
	// StartMeetingPreview takes precedence over StartMeeting when set.
	StartMeetingPreview func(ctx context.Context, id, device string, preview func(st.Preview)) (st.Meeting, error)
	StartMeeting        func(ctx context.Context, id, device string) (st.Meeting, error)
	FinalizeMeeting     func(ctx context.Context, bundle string) (text string, err error)
	// ValidateRetry must restrict paths to the configured meetings root, reject
	// escapes and verify a closed or recoverable bundle. Return its canonical path.
	ValidateRetry func(ctx context.Context, bundle string) (canonicalBundle string, err error)
}

// QuickClose splits microphone closure from final ASR. A Quick without this
// extension holds capture admission until its serialized Stop has returned.
type QuickClose interface{ CloseCapture(context.Context) error }

// MeetingLifecycle lets the controller distinguish admission, readiness and
// unexpected source closure. Status must be safe to call during capture.
type MeetingLifecycle interface {
	Ready() <-chan struct{}
	Done() <-chan struct{}
	Status() st.Status
}

type ErrorCode string

const (
	ErrBusy        ErrorCode = "busy"
	ErrNoSession   ErrorCode = "no_session"
	ErrQueueFull   ErrorCode = "queue_full"
	ErrUnavailable ErrorCode = "unavailable"
	ErrVersion     ErrorCode = "version_mismatch"
	ErrRequest     ErrorCode = "invalid_request"
	ErrCanceled    ErrorCode = "canceled"
	ErrCapture     ErrorCode = "capture_failed"
	ErrFinalize    ErrorCode = "finalize_failed"
	ErrDelivery    ErrorCode = "delivery_failed"
	ErrBundle      ErrorCode = "invalid_bundle"
	ErrNoResult    ErrorCode = "no_result"
)

// ReplyError preserves the protocol code when a client turns a reply into a Go error.
type ReplyError struct {
	Code   ErrorCode
	Detail string
}

func (e *ReplyError) Error() string {
	if e.Detail == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Detail
}

func ErrorFromReply(r st.Reply) error {
	if r.Error == "" {
		return nil
	}
	return &ReplyError{ErrorCode(r.Error), r.Status.Warning}
}

type controller struct {
	opts   Options
	ctx    context.Context
	cancel context.CancelFunc

	// Actions serialize producer ownership, but neither job waits nor UI
	// updates acquire this lock. mu protects snapshots and worker results.
	actions         sync.Mutex
	mu              sync.Mutex
	capture         *capture
	pending         []*job
	previousMeeting *job
	recentJob       string
	recentState     string
	warning         string
	lastText        string
	latestQuick     string

	jobs       chan *job
	workerDone chan struct{}
	changed    chan struct{}
	preview    *st.Preview
}

type capture struct {
	id, kind, phase, started, target, warning string
	cancel                                    context.CancelFunc
	quick                                     st.Quick
	meeting                                   st.Meeting
}

type job struct {
	id, captureID, bundle, target string
	quick                         st.Quick
	held                          *capture
	release                       context.CancelFunc
	ctx                           context.Context
	cancel                        context.CancelFunc
	done                          chan struct{}
	state                         string    // under controller.mu
	code                          ErrorCode // result fields are published by closing done
	text, detail                  string
	deliveryStarted               bool // under controller.mu
	textTooLarge                  bool
}

func newController(ctx context.Context, opts Options) *controller {
	ctx, cancel := context.WithCancel(ctx)
	c := &controller{opts: opts, ctx: ctx, cancel: cancel, jobs: make(chan *job, maxJobs), workerDone: make(chan struct{}), changed: make(chan struct{}, 1)}
	go c.work()
	return c
}

func (c *controller) statusLocked() st.Status {
	s := st.Status{Capture: "idle", Job: c.recentJob, JobState: c.recentState, Warning: c.warning}
	if cap := c.capture; cap != nil {
		s.Capture, s.Session, s.StartedUTC, s.Warning = cap.phase, cap.id, cap.started, cap.warning
	}
	if len(c.pending) > 0 {
		s.Job, s.JobState = c.pending[0].id, c.pending[0].state
	}
	return s
}

func (c *controller) reply(id, path, text string, code ErrorCode, detail string) st.Reply {
	c.mu.Lock()
	s := c.statusLocked()
	c.mu.Unlock()
	if code != "" {
		s.Warning = detail
	}
	return st.Reply{Version: st.Version, Status: s, ID: id, Path: path, Text: text, Error: string(code)}
}

func (c *controller) changedLocked() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

func (c *controller) setCaptureLocked(cap *capture) {
	c.capture = cap
	c.preview = nil
	c.changedLocked()
}

func (c *controller) publish(id string, p st.Preview) {
	// Preview is opt-in and lossy. In particular, do not let an old producer
	// erase the current caption when its asynchronous inference call returns.
	if c.opts.Desktop == nil || len(p.Stable)+len(p.Tentative) > st.MaxFrame {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cap := c.capture; cap != nil && cap.id == id && cap.phase != "stopping" {
		p.Session = id
		c.preview = &p
		c.changedLocked()
	}
}

func (c *controller) desktopUpdates() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.changed:
			c.mu.Lock()
			status := c.statusLocked()
			c.mu.Unlock()
			c.opts.Desktop.Update(status)
			// Update may have blocked behind the native thread. Recheck the
			// session, and let Update announce it before dispatching its caption.
			c.mu.Lock()
			p := c.preview
			if p != nil && c.capture != nil && c.capture.id == p.Session && p.Session == status.Session && c.capture.phase == status.Capture && (status.Capture == "dictating" || status.Capture == "meeting") {
				c.preview = nil
			} else {
				p = nil
			}
			c.mu.Unlock()
			if p != nil {
				c.opts.Desktop.Caption(*p)
			}
		}
	}
}

func validateRequest(r st.Request) (ErrorCode, string) {
	if r.Version != st.Version {
		return ErrVersion, fmt.Sprintf("server uses protocol version %d", st.Version)
	}
	switch r.Action {
	case "status", "quit", "last.copy", "dictate.start", "dictate.stop", "dictate.toggle", "dictate.cancel", "meeting.start", "meeting.stop", "meeting.retry":
	default:
		return ErrRequest, "unknown action"
	}
	if (r.Wait || r.Print != "") && r.Action != "meeting.stop" ||
		r.Device != "" && r.Action != "meeting.start" && r.Action != "dictate.start" && r.Action != "dictate.toggle" ||
		r.Bundle != "" && r.Action != "meeting.retry" ||
		r.Session != "" && r.Action != "meeting.stop" && r.Action != "dictate.stop" && r.Action != "dictate.cancel" {
		return ErrRequest, "unexpected fields for this action"
	}
	switch r.Print {
	case "", "path", "text", "none":
	default:
		return ErrRequest, "print must be path, text, or none"
	}
	return "", ""
}

func (c *controller) Do(ctx context.Context, r st.Request) st.Reply {
	// Once admitted, capture/stop/retry uses daemon-owned contexts. A client
	// cancel (or a full socket) only releases its own status/result wait.
	if code, detail := validateRequest(r); code != "" {
		return c.reply("", "", "", code, detail)
	}
	if ctx.Err() != nil {
		return c.reply("", "", "", ErrCanceled, ctx.Err().Error())
	}
	if r.Action == "status" {
		return c.reply("", "", "", "", "")
	}
	if r.Action == "quit" {
		c.cancel()
		return c.reply("", "", "", "", "")
	}
	response, j := c.action(ctx, r)
	if j == nil {
		return response
	}
	select {
	case <-ctx.Done():
		return c.reply(j.id, j.bundle, "", ErrCanceled, ctx.Err().Error())
	case <-c.ctx.Done():
		return c.reply(j.id, j.bundle, "", ErrCanceled, "daemon is shutting down")
	case <-j.done:
		var text string
		if r.Print == "text" && j.code == "" {
			if j.textTooLarge {
				return c.reply(j.id, j.bundle, "", ErrFinalize, "transcript exceeds control frame limit; use the meeting bundle")
			}
			text = j.text
		}
		return c.reply(j.id, j.bundle, text, j.code, j.detail)
	}
}

func (c *controller) action(ctx context.Context, r st.Request) (st.Reply, *job) {
	c.actions.Lock()
	defer c.actions.Unlock()
	if ctx.Err() != nil {
		return c.reply("", "", "", ErrCanceled, ctx.Err().Error()), nil
	}
	if c.ctx.Err() != nil {
		return c.reply("", "", "", ErrUnavailable, "daemon is shutting down"), nil
	}
	c.mu.Lock()
	cap := c.capture
	c.mu.Unlock()
	switch r.Action {
	case "dictate.start":
		return c.start("dictating", r.Device), nil
	case "meeting.start":
		return c.start("meeting", r.Device), nil
	case "dictate.toggle":
		if cap == nil || cap.kind != "dictating" {
			return c.start("dictating", r.Device), nil
		}
		return c.stopQuick(cap, "")
	case "dictate.stop":
		if r.Session != "" && (cap == nil || cap.id != r.Session) {
			return c.reply("", "", "", ErrNoSession, "dictation session is not active"), nil
		}
		if cap == nil {
			return c.reply("", "", "", "", ""), nil
		}
		if cap.kind != "dictating" {
			return c.reply("", "", "", ErrBusy, "a meeting is active"), nil
		}
		return c.stopQuick(cap, "")
	case "dictate.cancel":
		return c.cancelQuick(cap, r.Session), nil
	case "meeting.stop":
		return c.stopMeeting(cap, r)
	case "meeting.retry":
		return c.retry(cap, r.Bundle), nil
	case "last.copy":
		return c.copyLast(), nil
	default:
		return c.reply("", "", "", ErrRequest, "unknown action"), nil
	}
}

func (c *controller) start(kind, device string) st.Reply {
	c.mu.Lock()
	if cap := c.capture; cap != nil {
		id := cap.id
		same := cap.kind == kind && cap.phase != "stopping"
		c.mu.Unlock()
		if same {
			return c.reply(id, "", "", "", "")
		}
		return c.reply("", "", "", ErrBusy, "another capture is active or stopping")
	}
	if len(c.pending) >= maxJobs {
		c.mu.Unlock()
		return c.reply("", "", "", ErrQueueFull, "wait for background work before starting a capture")
	}
	c.mu.Unlock()
	if (kind == "meeting" && ((c.opts.StartMeeting == nil && c.opts.StartMeetingPreview == nil) || c.opts.FinalizeMeeting == nil)) || (kind == "dictating" && c.opts.StartQuick == nil) {
		return c.reply("", "", "", ErrUnavailable, "capture backend is not configured")
	}
	// Do not cancel a running meeting until Stop can write its manifest.
	ctx, cancel := context.WithCancel(context.WithoutCancel(c.ctx))
	cap := &capture{id: rand.Text(), kind: kind, phase: "starting", started: time.Now().UTC().Format(time.RFC3339Nano), cancel: cancel}
	c.mu.Lock()
	c.warning = ""
	c.setCaptureLocked(cap)
	c.mu.Unlock()
	abortStart := context.AfterFunc(c.ctx, cancel)
	defer abortStart()
	var err error
	notices := make(chan string, 1)
	if kind == "meeting" {
		if c.opts.StartMeetingPreview != nil {
			cap.meeting, err = c.opts.StartMeetingPreview(ctx, cap.id, device, func(p st.Preview) { c.publish(cap.id, p) })
		} else {
			cap.meeting, err = c.opts.StartMeeting(ctx, cap.id, device)
		}
		if cap.meeting == nil && err == nil {
			err = errors.New("meeting backend returned no capture")
		}
	} else {
		if c.opts.Desktop != nil {
			cap.target = c.opts.Desktop.Target()
		}
		var notify sync.Once
		cap.quick, err = c.opts.StartQuick(ctx, cap.id, device, func(p st.Preview) { c.publish(cap.id, p) }, func(reason string) {
			notify.Do(func() { notices <- reason })
		})
		if cap.quick == nil && err == nil {
			err = errors.New("dictation backend returned no capture")
		}
	}
	if err != nil {
		cancel()
		c.mu.Lock()
		c.warning = err.Error()
		c.setCaptureLocked(nil)
		c.mu.Unlock()
		return c.reply("", "", "", ErrCapture, err.Error())
	}
	lifecycle, observesMeeting := cap.meeting.(MeetingLifecycle)
	c.mu.Lock()
	if !observesMeeting {
		cap.phase = kind
	}
	if kind == "dictating" {
		c.latestQuick = cap.id
	}
	c.changedLocked()
	c.mu.Unlock()
	if observesMeeting {
		go c.watchMeeting(ctx, cap, lifecycle)
	}
	if kind == "dictating" {
		go func() {
			select {
			case <-ctx.Done():
			case <-c.ctx.Done():
			case reason := <-notices:
				c.actions.Lock()
				defer c.actions.Unlock()
				c.mu.Lock()
				current := c.capture == cap
				c.mu.Unlock()
				if current && c.ctx.Err() == nil {
					c.stopQuick(cap, reason)
				}
			}
		}()
	}
	return c.reply(cap.id, "", "", "", "")
}

func (c *controller) watchMeeting(ctx context.Context, cap *capture, life MeetingLifecycle) {
	ready := life.Ready()
	// Warnings can change without a lifecycle edge (e.g. one track drops).
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.ctx.Done():
			return
		case <-life.Done():
			c.actions.Lock()
			c.mu.Lock()
			current := c.capture == cap
			c.mu.Unlock()
			if current && c.ctx.Err() == nil {
				c.stopMeeting(cap, st.Request{Session: cap.id})
			}
			c.actions.Unlock()
			return
		case <-ready:
			ready = nil
		case <-tick.C:
		}
		warning := life.Status().Warning
		c.mu.Lock()
		if c.capture == cap && cap.phase != "stopping" {
			phase := "starting"
			if ready == nil {
				phase = "meeting"
			}
			if cap.phase != phase || cap.warning != warning {
				cap.phase, cap.warning = phase, warning
				c.changedLocked()
			}
		}
		c.mu.Unlock()
	}
}

func (c *controller) stopping(cap *capture, warning string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cap.phase = "stopping"
	cap.warning = warning
	c.preview = nil
	c.changedLocked()
}

func (c *controller) clearCapture(cap *capture, warning string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capture == cap {
		c.warning = warning
		c.setCaptureLocked(nil)
	}
}

func (c *controller) stopQuick(cap *capture, reason string) (st.Reply, *job) {
	c.mu.Lock()
	already := cap.phase == "stopping"
	var existing *job
	if already {
		for _, j := range c.pending {
			if j.held == cap {
				existing = j
				break
			}
		}
	}
	c.mu.Unlock()
	if already {
		return c.reply(cap.id, "", "", "", ""), existing
	}
	c.stopping(cap, reason)
	j := c.newJob(cap.id, "")
	j.quick, j.target, j.release = cap.quick, cap.target, cap.cancel
	if closer, ok := cap.quick.(QuickClose); ok {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.ctx), stopTimeout)
		err := closer.CloseCapture(ctx)
		cancel()
		if err != nil {
			j.cancel()
			cap.cancel()
			cap.quick.Cancel()
			c.clearCapture(cap, err.Error())
			return c.reply(cap.id, "", "", ErrCapture, err.Error()), nil
		}
		c.enqueue(j, cap, reason)
		return c.reply(cap.id, "", "", "", ""), nil
	}
	j.held = cap
	c.enqueue(j, nil, "")
	return c.reply(cap.id, "", "", "", ""), j
}

func (c *controller) cancelQuick(cap *capture, id string) st.Reply {
	if cap != nil && cap.kind == "dictating" && (id == "" || id == cap.id) {
		c.mu.Lock()
		held := cap.phase == "stopping"
		c.mu.Unlock()
		if !held {
			c.stopping(cap, "")
			cap.cancel()
			cap.quick.Cancel()
			c.clearCapture(cap, "")
			return c.reply(cap.id, "", "", "", "")
		}
	}
	c.mu.Lock()
	var pending *job
	for _, j := range c.pending {
		if j.quick != nil && (id == "" || id == j.captureID) {
			pending = j
		}
	}
	if pending != nil && pending.deliveryStarted {
		c.mu.Unlock()
		return c.reply(pending.captureID, "", "", ErrBusy, "delivery has already started")
	}
	if pending != nil {
		pending.cancel()
	}
	c.mu.Unlock()
	if pending != nil {
		return c.reply(pending.captureID, "", "", "", "")
	}
	if id != "" {
		return c.reply("", "", "", ErrNoSession, "dictation session is not active or pending")
	}
	if cap != nil && cap.kind == "meeting" {
		return c.reply("", "", "", ErrBusy, "a meeting is active")
	}
	return c.reply("", "", "", "", "")
}

func (c *controller) stopMeeting(cap *capture, r st.Request) (st.Reply, *job) {
	var j *job
	if cap != nil && cap.kind == "meeting" && (r.Session == "" || r.Session == cap.id) {
		warning := ""
		if life, ok := cap.meeting.(MeetingLifecycle); ok {
			warning = life.Status().Warning
		}
		c.stopping(cap, warning)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.ctx), stopTimeout)
		path, err := cap.meeting.Stop(ctx)
		timedOut := ctx.Err() != nil
		cancel()
		if err != nil && timedOut {
			c.mu.Lock()
			cap.warning = err.Error()
			c.changedLocked()
			c.mu.Unlock()
			return c.reply(cap.id, path, "", ErrCapture, err.Error()), nil
		}
		cap.cancel()
		if path == "" && err == nil {
			err = errors.New("meeting closed without a bundle path")
		}
		if err != nil {
			c.clearCapture(cap, err.Error())
			return c.reply(cap.id, path, "", ErrCapture, err.Error()), nil
		}
		j = c.newJob(cap.id, path)
		c.previousMeeting = j
		c.enqueue(j, cap, warning)
	} else if r.Session != "" {
		c.mu.Lock()
		for _, candidate := range c.pending {
			if candidate.captureID == r.Session && candidate.bundle != "" {
				j = candidate
				break
			}
		}
		c.mu.Unlock()
		if j == nil && c.previousMeeting != nil && c.previousMeeting.captureID == r.Session {
			j = c.previousMeeting
		}
	}
	if j == nil {
		if cap != nil && cap.kind != "meeting" {
			return c.reply("", "", "", ErrBusy, "dictation is active"), nil
		}
		return c.reply("", "", "", ErrNoSession, "no active meeting"), nil
	}
	if r.Wait || r.Print == "text" {
		return st.Reply{}, j
	}
	return c.reply(j.id, j.bundle, "", "", ""), nil
}

func (c *controller) retry(cap *capture, bundle string) st.Reply {
	if bundle == "" {
		return c.reply("", "", "", ErrBundle, "a meeting bundle is required")
	}
	if c.opts.ValidateRetry == nil || c.opts.FinalizeMeeting == nil {
		return c.reply("", "", "", ErrUnavailable, "meeting retry is not configured")
	}
	ctx, cancel := context.WithTimeout(c.ctx, stopTimeout)
	defer cancel()
	canonical, err := c.opts.ValidateRetry(ctx, bundle)
	if err != nil {
		return c.reply("", "", "", ErrBundle, err.Error())
	}
	if canonical == "" {
		return c.reply("", "", "", ErrBundle, "bundle validator returned no path")
	}
	if cap != nil && cap.meeting != nil && cap.meeting.Bundle() == canonical {
		return c.reply("", "", "", ErrBusy, "cannot retry an active meeting")
	}
	c.mu.Lock()
	for _, j := range c.pending {
		if j.bundle == canonical {
			c.mu.Unlock()
			return c.reply(j.id, canonical, "", "", "")
		}
	}
	reserved := cap != nil
	for _, j := range c.pending {
		if j.held == cap && cap != nil {
			reserved = false
			break
		}
	}
	full := len(c.pending) >= maxJobs || (reserved && len(c.pending) >= maxJobs-1)
	c.mu.Unlock()
	if full {
		return c.reply("", "", "", ErrQueueFull, "background work queue is full")
	}
	j := c.newJob("", canonical)
	c.enqueue(j, nil, "")
	return c.reply(j.id, canonical, "", "", "")
}

func (c *controller) copyLast() st.Reply {
	if c.opts.Desktop == nil {
		return c.reply("", "", "", ErrUnavailable, "no desktop clipboard adapter")
	}
	c.mu.Lock()
	text := c.lastText
	c.mu.Unlock()
	if text == "" {
		return c.reply("", "", "", ErrNoResult, "no saved dictation result")
	}
	ctx, cancel := context.WithTimeout(c.ctx, stopTimeout)
	defer cancel()
	if err := c.opts.Desktop.Deliver(ctx, text, "", false); err != nil {
		return c.reply("", "", "", ErrDelivery, err.Error())
	}
	return c.reply("", "", "", "", "")
}

func (c *controller) newJob(captureID, bundle string) *job {
	ctx, cancel := context.WithCancel(c.ctx)
	return &job{id: rand.Text(), captureID: captureID, bundle: bundle, ctx: ctx, cancel: cancel, done: make(chan struct{}), state: "queued"}
}

func (c *controller) enqueue(j *job, cap *capture, warning string) {
	c.mu.Lock()
	c.pending = append(c.pending, j)
	if cap != nil && c.capture == cap {
		c.warning = warning
		c.setCaptureLocked(nil)
	}
	c.changedLocked()
	c.mu.Unlock()
	c.jobs <- j
}

func (c *controller) work() {
	defer close(c.workerDone)
	for j := range c.jobs {
		c.mu.Lock()
		j.state = "running"
		c.changedLocked()
		c.mu.Unlock()
		c.runJob(j)
		if j.release != nil {
			j.release()
		}
		j.cancel()
		c.mu.Lock()
		if j.held != nil && c.capture == j.held {
			c.warning = j.held.warning
			c.setCaptureLocked(nil)
		}
		c.pending[0] = nil
		c.pending = c.pending[1:]
		c.recentJob = j.id
		switch j.code {
		case "", ErrDelivery:
			c.recentState = "ready"
		case ErrCanceled:
			c.recentState = "canceled"
		default:
			c.recentState = "failed"
		}
		if j.detail != "" && c.capture == nil {
			c.warning = j.detail
		}
		close(j.done)
		c.changedLocked()
		c.mu.Unlock()
	}
}

func (c *controller) runJob(j *job) {
	if j.ctx.Err() != nil {
		if j.quick != nil {
			j.quick.Cancel()
		}
		j.code = ErrCanceled
		return
	}
	var text string
	var err error
	if j.quick != nil {
		text, err = j.quick.Stop(j.ctx)
		if j.held != nil {
			if err != nil || j.ctx.Err() != nil {
				// Without separate closure, force cleanup before releasing admission.
				j.quick.Cancel()
			}
			c.clearCapture(j.held, j.held.warning)
		}
	} else {
		text, err = c.opts.FinalizeMeeting(j.ctx, j.bundle)
	}
	if j.ctx.Err() != nil {
		j.code = ErrCanceled
		return
	}
	if err != nil {
		j.code, j.detail = ErrFinalize, err.Error()
		return
	}
	if j.quick == nil {
		j.textTooLarge = len(text) >= st.MaxFrame
		if !j.textTooLarge {
			j.text = text
		}
		return
	}
	if strings.TrimSpace(text) != "" {
		c.deliverQuick(j, text)
	}
}

func (c *controller) deliverQuick(j *job, text string) {
	c.mu.Lock()
	if j.ctx.Err() != nil {
		c.mu.Unlock()
		j.code = ErrCanceled
		return
	}
	j.deliveryStarted = true
	paste := c.opts.Paste && c.latestQuick == j.captureID && c.capture == nil && j.target != ""
	c.mu.Unlock()
	if c.opts.Desktop == nil {
		j.code, j.detail = ErrDelivery, "dictation finished without a desktop clipboard adapter"
		return
	}
	if len(text) >= st.MaxFrame {
		j.code, j.detail = ErrDelivery, "dictation text exceeds the clipboard limit"
		return
	}
	ctx, cancel := context.WithTimeout(j.ctx, stopTimeout)
	defer cancel()
	if ctx.Err() != nil {
		j.code = ErrCanceled
		return
	}
	if err := c.opts.Desktop.Deliver(ctx, text, j.target, paste); err != nil {
		if j.ctx.Err() != nil {
			j.code = ErrCanceled
			return
		}
		j.code, j.detail = ErrDelivery, err.Error()
	} else if c.opts.Paste && !paste {
		j.detail = "copied, not pasted: original focus cannot be verified"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if j.ctx.Err() != nil {
		j.code = ErrCanceled
		return
	}
	c.lastText = text
}

func (c *controller) shutdown() error {
	c.cancel()
	done := make(chan error, 1)
	go func() {
		c.actions.Lock()
		c.mu.Lock()
		cap := c.capture
		c.mu.Unlock()
		var err error
		if cap != nil {
			if cap.meeting != nil {
				c.stopping(cap, "")
				ctx, cancel := context.WithTimeout(context.WithoutCancel(c.ctx), stopTimeout)
				_, err = cap.meeting.Stop(ctx)
				cancel()
				cap.cancel()
			} else if cap.phase != "stopping" {
				cap.cancel()
				cap.quick.Cancel()
			}
		}
		close(c.jobs)
		c.actions.Unlock()
		<-c.workerDone
		done <- err
	}()
	timer := time.NewTimer(stopTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errors.New("desktop daemon shutdown timed out")
	}
}
