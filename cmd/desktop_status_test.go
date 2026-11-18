//go:build linux || darwin

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joegoldin/audiomemo/internal/desktopservice"
	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/spf13/cobra"
)

func desktopTestRoot(factory daemonFactory) *cobra.Command {
	cmd := &cobra.Command{Use: "audiomemo", SilenceUsage: true, SilenceErrors: true}
	cmd.AddCommand(newDaemonCommand(factory), newDesktopStatusCommand(), newDictateCommand(), newMeetingCommand(), newQuitCommand())
	return cmd
}

func invokeDesktop(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := desktopTestRoot(func(context.Context, string) (desktopservice.Options, error) {
		return desktopservice.Options{}, errors.New("not configured")
	})
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(t.Context())
	return out.String(), stderr.String(), err
}

func desktopTestRuntime(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "am-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("AUDIOMEMO_RUNTIME_DIR", filepath.Join(dir, "daemon"))
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
}

type desktopTestMeeting struct{ path string }

func (m desktopTestMeeting) Bundle() string                       { return m.path }
func (m desktopTestMeeting) Stop(context.Context) (string, error) { return m.path, nil }

type unexpectedDesktop struct{ t *testing.T }

func (d unexpectedDesktop) Run(context.Context, st.Controller) error {
	d.t.Error("--headless ran the desktop")
	return errors.New("unexpected desktop")
}
func (unexpectedDesktop) Update(st.Status)   {}
func (unexpectedDesktop) Caption(st.Preview) {}
func (unexpectedDesktop) Target() string     { return "" }
func (unexpectedDesktop) Deliver(context.Context, string, string, bool) error {
	return errors.New("unexpected clipboard")
}

func startDesktopCommand(t *testing.T, opts desktopservice.Options) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	cmd := desktopTestRoot(func(_ context.Context, path string) (desktopservice.Options, error) {
		if path != "fake-config" {
			t.Errorf("factory config = %q", path)
		}
		opts.Desktop = unexpectedDesktop{t}
		return opts, nil
	})
	cmd.SetArgs([]string{"daemon", "--headless", "-c", "fake-config"})
	go func() { done <- cmd.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, err := invokeDesktop(t, "status", "--json"); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("daemon never became ready")
}

func TestDesktopCommandsOutputAndUnavailable(t *testing.T) {
	desktopTestRuntime(t)
	if out, diag, err := invokeDesktop(t, "status"); out != "" || diag != "" || err == nil || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatal(out, diag, err)
	}
	if _, err := os.Stat(os.Getenv("AUDIOMEMO_RUNTIME_DIR")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status created the runtime directory", err)
	}
	const bundle = "/mock/closed-bundle"
	runs := 0
	startDesktopCommand(t, desktopservice.Options{
		StartMeeting: func(_ context.Context, id, group string) (st.Meeting, error) {
			if group != "combo" {
				t.Errorf("group = %q", group)
			}
			return desktopTestMeeting{bundle}, nil
		},
		FinalizeMeeting: func(context.Context, string) (string, error) {
			runs++
			if runs == 3 {
				return "", errors.New("local model unavailable")
			}
			return "speaker: transcript\n", nil
		},
	})
	out, diag, err := invokeDesktop(t, "status", "--json")
	var status st.Status
	if err != nil || diag != "" || json.Unmarshal([]byte(out), &status) != nil || status.Capture != "idle" {
		t.Fatal(out, diag, err)
	}
	if out, diag, err := invokeDesktop(t, "meeting", "stop"); out != "" || diag != "" || err == nil {
		t.Fatal(out, diag, err)
	}
	for i, mode := range []string{"path", "text", "text"} {
		if out, diag, err := invokeDesktop(t, "meeting", "start", "-D", "combo"); strings.TrimSpace(out) == "" || diag != "" || err != nil {
			t.Fatal(out, diag, err)
		}
		out, diag, err := invokeDesktop(t, "meeting", "stop", "--wait", "--print", mode)
		if i == 2 {
			if out != "" || diag != "" || err == nil || !strings.Contains(err.Error(), bundle) || !strings.Contains(err.Error(), "local model unavailable") {
				t.Fatal(out, diag, err)
			}
			continue
		}
		want := bundle + "\n"
		if mode == "text" {
			want = "speaker: transcript\n"
		}
		if out != want || diag != "" || err != nil {
			t.Fatal(out, diag, err)
		}
	}
	if out, diag, err := invokeDesktop(t, "quit"); out != "" || diag != "" || err != nil {
		t.Fatal(out, diag, err)
	}
}

// The child executes the real Cobra command with OS pipes and a real SIGINT.
func TestDesktopWaitingClientProcess(t *testing.T) {
	if os.Getenv("AUDIOMEMO_TEST_WAIT_CLIENT") != "1" {
		return
	}
	cmd := desktopTestRoot(nil)
	cmd.SetArgs([]string{"meeting", "stop", "--print", "text"})
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestDesktopSIGINTOnlyCancelsPipedWait(t *testing.T) {
	desktopTestRuntime(t)
	entered := make(chan struct{})
	allowFinal := make(chan struct{})
	completed := make(chan struct{})
	startDesktopCommand(t, desktopservice.Options{
		StartMeeting: func(context.Context, string, string) (st.Meeting, error) {
			return desktopTestMeeting{"/mock/first"}, nil
		},
		FinalizeMeeting: func(ctx context.Context, _ string) (string, error) {
			close(entered)
			select {
			case <-allowFinal:
				close(completed)
				return "private transcript", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	})
	if _, _, err := invokeDesktop(t, "meeting", "start"); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p := exec.CommandContext(ctx, self, "-test.run=^TestDesktopWaitingClientProcess$")
	p.Env = append(os.Environ(), "AUDIOMEMO_TEST_WAIT_CLIENT=1")
	var out, diag bytes.Buffer
	p.Stdout, p.Stderr = &out, &diag
	if err = p.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		p.Wait()
		t.Fatal("finalizer never started")
	}
	if _, _, err := invokeDesktop(t, "meeting", "start"); err != nil {
		t.Error(err)
	}
	if err = p.Process.Signal(os.Interrupt); err != nil {
		t.Error(err)
	}
	var exitErr *exec.ExitError
	if err = p.Wait(); !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "context canceled") {
		t.Fatal(err, out.String(), diag.String())
	}
	close(allowFinal)
	select {
	case <-completed:
	case <-ctx.Done():
		t.Fatal("client interrupt canceled finalizer")
	}
	if out, _, err := invokeDesktop(t, "status", "--json"); err != nil || !strings.Contains(out, `"capture":"meeting"`) {
		t.Fatal(out, err)
	}
}
