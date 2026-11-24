//go:build linux

package desktoplinux

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
)

type controllerFunc func(context.Context, sessiontypes.Request) sessiontypes.Reply

func (f controllerFunc) Do(ctx context.Context, req sessiontypes.Request) sessiontypes.Reply {
	return f(ctx, req)
}

func TestDesktopBridge(t *testing.T) {
	d := New()
	d.command = func(ctx context.Context) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFakeHelper$")
		cmd.Env = append(os.Environ(), "AUDIOMEMO_TEST_HELPER=1")
		return cmd, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	actions := make(chan sessiontypes.Request, 5)
	done := make(chan error, 1)
	d.Update(sessiontypes.Status{Capture: "dictating", Session: "new"})
	d.Caption(sessiontypes.Preview{Session: "new", Stable: "stable", Tentative: " tentative"})
	d.Caption(sessiontypes.Preview{Session: "old", Stable: "stale"})
	go func() {
		done <- d.Run(ctx, controllerFunc(func(ctx context.Context, req sessiontypes.Request) sessiontypes.Reply {
			actions <- req
			if req.Action == "last.copy" {
				if err := d.Deliver(ctx, "menu copy", "", false); err != nil {
					t.Errorf("copy from menu: %v", err)
				}
			}
			return sessiontypes.Reply{}
		}))
	}()
	waitAction := func(action string) {
		t.Helper()
		select {
		case req := <-actions:
			if req.Version != sessiontypes.Version || req.Action != action {
				t.Fatalf("request = %+v, want %s", req, action)
			}
		case err := <-done:
			t.Fatalf("helper ended before %s: %v", action, err)
		case <-ctx.Done():
			t.Fatalf("missing action %s", action)
		}
	}
	waitAction("last.copy")
	if d.Target() != "" {
		t.Fatal("Linux must not guess focus identity")
	}
	if err := d.Deliver(ctx, "denied", "", false); err == nil || errors.Is(err, ErrPasteUnavailable) {
		t.Fatalf("clipboard denial = %v", err)
	}
	if err := d.Deliver(ctx, "clipboard\ntext", "guessed", true); !errors.Is(err, ErrPasteUnavailable) {
		t.Fatalf("paste fallback = %v", err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err := d.Deliver(canceled, "must not be sent", "", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled delivery = %v", err)
	}
	d.Update(sessiontypes.Status{Capture: "meeting", Session: "meeting-01"})
	d.Caption(sessiontypes.Preview{Session: "meeting-01", Stable: "meeting draft"})
	d.Caption(sessiontypes.Preview{Session: "new", Stable: "old Quick Scribe"})
	waitAction("meeting.stop")
	d.Update(sessiontypes.Status{Capture: "stopping", Session: "meeting-01"})
	d.Caption(sessiontypes.Preview{Session: "meeting-01", Stable: "late draft"})
	waitAction("status")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := d.Deliver(context.Background(), "after shutdown", "", false); err == nil {
		t.Fatal("delivery succeeded after helper shutdown")
	}
}

func TestNoDisplay(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	d := New()
	if err := d.Run(context.Background(), nil); !errors.Is(err, errUnavailable) {
		t.Fatalf("headless Run = %v", err)
	}
	if err := d.Deliver(context.Background(), "fixture", "", false); !errors.Is(err, errUnavailable) {
		t.Fatalf("headless Deliver = %v", err)
	}
}

func TestFakeHelper(t *testing.T) {
	if os.Getenv("AUDIOMEMO_TEST_HELPER") != "1" {
		return
	}
	fmt.Println(`{"type":"ready"}`)
	seen := make(map[string]bool)
	actOnce := func(action string) {
		if !seen[action] {
			seen[action] = true
			fmt.Printf("{\"type\":\"action\",\"action\":%q}\n", action)
		}
	}
	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		var m struct {
			Type    string
			Status  viewStatus
			Preview sessiontypes.Preview
			ID      uint64
			Text    string
		}
		if err := json.Unmarshal(input.Bytes(), &m); err != nil {
			os.Exit(2)
		}
		switch m.Type {
		case "view":
			switch m.Status.Capture {
			case "dictating":
				if m.Status.Session != "new" || m.Preview.Stable != "stable" || m.Preview.Tentative != " tentative" {
					os.Exit(3)
				}
				actOnce("last.copy")
			case "meeting":
				if m.Preview.Session == "" {
					continue
				}
				if m.Preview.Session != "meeting-01" || m.Preview.Stable != "meeting draft" {
					os.Exit(6)
				}
				actOnce("meeting.stop")
			case "stopping":
				if m.Preview.Session != "" {
					os.Exit(4)
				}
				actOnce("arbitrary")
				actOnce("status")
			}
		case "copy":
			switch m.Text {
			case "denied":
				fmt.Printf("{\"type\":\"copied\",\"id\":%d,\"code\":\"unavailable\"}\n", m.ID)
			case "menu copy", "clipboard\ntext":
				fmt.Printf("{\"type\":\"copied\",\"id\":%d}\n", m.ID)
			default:
				os.Exit(5)
			}
		}
	}
	os.Exit(0)
}

func TestSplitCaptionSurvivesFinalizingButNotNextSession(t *testing.T) {
	d := New()
	d.Update(sessiontypes.Status{Capture: "dictating", Session: "first", QuickSession: "first", QuickState: "recording"})
	want := sessiontypes.Preview{Session: "first", Split: true, Them: sessiontypes.CaptionLane{Stable: "remote words"}}
	d.Caption(want)
	d.Update(sessiontypes.Status{Capture: "idle", QuickSession: "first", QuickState: "finalizing"})
	if d.preview != want {
		t.Fatal("capture closure dropped caption")
	}
	d.Update(sessiontypes.Status{Capture: "starting", Session: "second", QuickSession: "second", QuickState: "starting"})
	if d.preview != (sessiontypes.Preview{}) {
		t.Fatal("a new capture carried over the previous caption")
	}
}
