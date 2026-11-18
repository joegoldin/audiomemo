package desktopmac

import (
	"errors"
	"fmt"
	"testing"
	"time"

	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
)

func TestCaptionFollowsActiveCaptureOnly(t *testing.T) {
	var s viewState
	s.update(st.Status{Capture: "dictating", Session: "first"})
	s.caption(st.Preview{Session: "first", Stable: "first", Tentative: "draft"})
	s.caption(st.Preview{Session: "old", Stable: "late"})
	if s.preview.Stable != "first" || s.preview.Tentative != "draft" {
		t.Fatal(s.preview)
	}
	s.update(st.Status{Capture: "dictating", Session: "first", JobState: "running"})
	if s.preview.Stable != "first" {
		t.Fatal("background job cleared live caption")
	}
	s.update(st.Status{Capture: "idle"})
	s.caption(st.Preview{Session: "first", Stable: "late"})
	if s.preview != (st.Preview{}) {
		t.Fatal("late preview resurrected stopped caption")
	}
	s.update(st.Status{Capture: "starting", Session: "second"})
	s.caption(st.Preview{Session: "second", Stable: "early"})
	if s.preview != (st.Preview{}) {
		t.Fatal("meeting draft appeared before readiness")
	}
	s.update(st.Status{Capture: "meeting", Session: "second"})
	s.caption(st.Preview{Session: "second", Stable: "meeting draft"})
	s.caption(st.Preview{Session: "first", Stable: "old dictation"})
	if s.preview.Stable != "meeting draft" {
		t.Fatal(s.preview)
	}
	s.update(st.Status{Capture: "stopping", Session: "second"})
	s.caption(st.Preview{Session: "second", Stable: "late meeting draft"})
	if s.preview != (st.Preview{}) {
		t.Fatal("late draft resurrected stopped meeting caption")
	}
}

func TestMenuUsesCaptureNotBackgroundJob(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 34, 56, 0, time.UTC)
	var s viewState
	s.update(st.Status{Capture: "meeting", Session: "active", StartedUTC: now.Add(-65 * time.Second).Format(time.RFC3339), Job: "old", JobState: "failed", Warning: "Input lost"})
	v := s.render(now)
	if v.title != "● 01:05 ⚠" || v.meeting != "meeting.stop" || v.dictate != "" || v.warning != "Input lost" {
		t.Fatal(v)
	}
	s.update(st.Status{Capture: "idle", JobState: "running"})
	v = s.render(now)
	if v.title != "AudioMemo" || v.meeting != "meeting.start" || v.dictate != "dictate.start" {
		t.Fatal(v)
	}
}

func TestPasteFallbackSaysWhatWasActuallyCopied(t *testing.T) {
	for _, tc := range []struct {
		result deliveryResult
		copied bool
		cause  error
	}{
		{deliveryOK, true, nil},
		{deliveryChanged, true, ErrFocusChanged},
		{deliveryTargetUnknown, true, ErrFocusUnknown},
		{deliveryDenied, true, ErrAccessibilityDenied},
		{deliveryModifiers, true, ErrKeyboardBusy},
		{deliveryClipboardChanged, false, ErrClipboardChanged},
		{deliveryInactive, false, ErrInactiveSession},
		{deliveryClipboardFailed, false, ErrClipboardUnavailable},
	} {
		copied, err := tc.result.outcome()
		if copied != tc.copied || !errors.Is(err, tc.cause) {
			t.Errorf("result %v: got copied=%v error=%v", tc.result, copied, err)
		}
	}
}

func TestSystemAudioPermissionMessagePreservesOtherCoreAudioErrors(t *testing.T) {
	denied := fmt.Errorf("system audio: create process tap: Core Audio status %d. Allow audio recording", 0x21686f67)
	if got := DescribeSystemAudioError(denied); !errors.Is(got, ErrSystemAudioDenied) || !errors.Is(got, denied) {
		t.Fatal(got)
	}
	unsupported := errors.New("system audio: create process tap: Core Audio status 1970171760. Allow audio recording")
	if got := DescribeSystemAudioError(unsupported); got != unsupported {
		t.Fatal(got)
	}
}
