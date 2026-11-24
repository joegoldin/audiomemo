package desktopmac

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
)

var (
	ErrUnavailable          = errors.New("AudioMemo's macOS desktop requires a CGO-enabled Darwin build and a logged-in graphical session")
	ErrInactiveSession      = errors.New("desktop session is locked or inactive; nothing was copied or pasted")
	ErrMicrophoneDenied     = errors.New("microphone access denied; allow AudioMemo in System Settings > Privacy & Security > Microphone")
	ErrMicrophoneRestricted = errors.New("microphone access restricted by macOS or device management")
	ErrSystemAudioDenied    = errors.New("system audio access denied; allow AudioMemo in System Settings > Privacy & Security > Screen & System Audio Recording")
	ErrAccessibilityDenied  = errors.New("keyboard posting or Accessibility access denied; allow AudioMemo in System Settings > Privacy & Security > Accessibility")
	ErrFocusUnknown         = errors.New("the original editable field could not be verified")
	ErrFocusChanged         = errors.New("the original app, window or field is no longer focused")
	ErrKeyboardBusy         = errors.New("a keyboard modifier is still held")
	ErrClipboardChanged     = errors.New("the clipboard changed before paste; newer clipboard contents were left alone")
	ErrClipboardUnavailable = errors.New("could not write the macOS clipboard")
	ErrPasteUnavailable     = errors.New("could not create a verified macOS paste shortcut for the active keyboard layout")
)

// These results match AMDelivery in desktop_darwin.h.
type deliveryResult int

const (
	deliveryOK deliveryResult = iota
	deliveryInactive
	deliveryClipboardFailed
	deliveryDenied
	deliveryTargetUnknown
	deliveryChanged
	deliveryModifiers
	deliveryClipboardChanged
	deliveryPasteUnavailable
	deliveryCanceled
	deliveryCopiedCanceled
)

func (r deliveryResult) outcome() (copied bool, err error) {
	switch r {
	case deliveryOK:
		return true, nil
	case deliveryInactive:
		return false, ErrInactiveSession
	case deliveryClipboardFailed:
		return false, ErrClipboardUnavailable
	case deliveryDenied:
		err = ErrAccessibilityDenied
	case deliveryTargetUnknown:
		err = ErrFocusUnknown
	case deliveryChanged:
		err = ErrFocusChanged
	case deliveryModifiers:
		err = ErrKeyboardBusy
	case deliveryClipboardChanged:
		return false, ErrClipboardChanged
	case deliveryCanceled:
		return false, context.Canceled
	case deliveryCopiedCanceled:
		err = context.Canceled
	case deliveryPasteUnavailable:
		err = ErrPasteUnavailable
	default:
		return false, ErrUnavailable
	}
	return true, fmt.Errorf("copied, not pasted: %w", err)
}

type viewState struct {
	status     st.Status
	preview    st.Preview
	notice     string
	phaseSince time.Time
}

func (s *viewState) update(status st.Status) {
	if status.QuickSession != s.status.QuickSession || status.QuickState != s.status.QuickState {
		s.phaseSince = time.Now()
	}
	keepQuick := status.QuickState == "finalizing" && status.QuickSession != "" && status.QuickSession == s.preview.Session
	if !keepQuick && (!hasCaption(status.Capture) || status.Capture != s.status.Capture || status.Session != s.status.Session) {
		s.preview = st.Preview{}
	}
	if status.Session != s.status.Session {
		s.notice = ""
	}
	s.status = status
}

func (s *viewState) caption(p st.Preview) {
	if p.Session != "" && hasCaption(s.status.Capture) && s.status.Session == p.Session {
		s.preview = p
	}
}

func (s viewState) overlay(now time.Time) (st.Preview, string) {
	if s.status.Capture == "meeting" {
		p := s.preview
		p.Session = s.status.Session
		return p, "meeting"
	}
	state, id := s.status.QuickState, s.status.QuickSession
	switch state {
	case "copied", "ready", "failed":
		if now.Sub(s.phaseSince) < 4200*time.Millisecond {
			return st.Preview{Session: id}, state
		}
	case "starting", "recording", "finalizing":
		p := s.preview
		if p.Session != id {
			p = st.Preview{Session: id}
		}
		p.Split = p.Split || s.status.QuickSplit
		return p, state
	}
	return st.Preview{}, ""
}

func hasCaption(capture string) bool {
	return capture == "dictating" || capture == "meeting"
}

type trayIcon int

const (
	trayIdle trayIcon = iota
	trayRecording
	trayRefining
)

func (s viewState) trayIcon() trayIcon {
	switch {
	case s.status.Capture == "dictating" || s.status.Capture == "meeting":
		return trayRecording
	case s.status.QuickState == "finalizing":
		return trayRefining
	default:
		return trayIdle
	}
}

type menuState struct {
	icon    trayIcon
	title   string
	status  string
	warning string
	dictate string
	meeting string
	cancel  bool
}

func (s viewState) render(now time.Time) menuState {
	v := menuState{icon: s.trayIcon(), title: "AudioMemo", status: "Ready", warning: s.status.Warning}
	switch s.status.Capture {
	case "dictating", "meeting":
		started, err := time.Parse(time.RFC3339Nano, s.status.StartedUTC)
		elapsed := max(0, int64(now.Sub(started)/time.Second))
		v.title = "● Rec"
		if err == nil {
			v.title = fmt.Sprintf("● %02d:%02d", elapsed/60, elapsed%60)
		}
		if s.status.Capture == "dictating" {
			v.status, v.dictate, v.cancel = "Quick Scribe recording", "dictate.stop", true
		} else {
			v.status, v.meeting = "Meeting recording", "meeting.stop"
			if s.preview.Session != "" {
				v.status += " · Live draft"
			}
		}
	case "starting":
		v.status = "Opening audio sources…"
	case "stopping":
		v.status = "Closing audio sources…"
	case "", "idle":
		v.dictate, v.meeting = "dictate.start", "meeting.start"
	default:
		v.status = "Capture unavailable"
	}
	if s.status.JobState != "" {
		v.status += " · Background: " + s.status.JobState
	}
	if s.notice != "" && s.notice != v.warning {
		v.warning = strings.Trim(v.warning+"\n"+s.notice, "\n")
	}
	if v.warning != "" {
		v.title += " ⚠"
	}
	return v
}
