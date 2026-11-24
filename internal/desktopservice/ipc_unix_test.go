//go:build linux || darwin

package desktopservice

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
)

func shortRuntime(t *testing.T) string {
	t.Helper()
	// Darwin has a short sockaddr_un.sun_path even in the test environment.
	path, err := os.MkdirTemp("", "am-g-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(path) })
	return filepath.Join(path, "desktop")
}

func startTestRun(t *testing.T, opts Options) (Client, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts); close(done) }()
	t.Cleanup(func() {
		cancel()
		if err := receive(t, done); err != nil {
			t.Error(err)
		}
	})
	c := Client{RuntimeDir: opts.RuntimeDir}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.Do(ctx, st.Request{Version: st.Version, Action: "status"}); err == nil {
			return c, done
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("daemon did not become ready")
	return c, done
}

func TestIPCVersionQuitAndShutdownRecovery(t *testing.T) {
	stopped := make(chan string, 1)
	opts := Options{RuntimeDir: shortRuntime(t), StartMeeting: func(context.Context, string, string) (st.Meeting, error) {
		return stopProbe{stopped}, nil
	}, FinalizeMeeting: func(context.Context, string) (string, error) { t.Error("quit finalized capture"); return "", nil }}
	c, done := startTestRun(t, opts)
	_, err := c.Do(t.Context(), st.Request{Version: st.Version + 1, Action: "status"})
	var replyErr *ReplyError
	if !errors.As(err, &replyErr) || replyErr.Code != ErrVersion {
		t.Fatal(err)
	}
	if r, err := c.Do(t.Context(), st.Request{Version: st.Version, Action: "meeting.start"}); err != nil || r.Status.Capture != "meeting" {
		t.Fatal(r, err)
	}
	if _, err := c.Do(t.Context(), st.Request{Version: st.Version, Action: "quit"}); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	if path := receive(t, stopped); path != "/recoverable" {
		t.Fatal(path)
	}
	if _, err := c.Do(t.Context(), st.Request{Version: st.Version, Action: "status"}); err == nil || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatal(err)
	}
}

type stopProbe struct{ stopped chan string }

func (m stopProbe) Stop(ctx context.Context) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	m.stopped <- m.Bundle()
	return m.Bundle(), nil
}
func (m stopProbe) Bundle() string { return "/recoverable" }

func TestIPCPrivatePathsAndStaleSocket(t *testing.T) {
	dir := shortRuntime(t)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := listen(dir); err == nil {
		t.Fatal("public runtime directory accepted")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, socketName)
	if err := os.Symlink("not-ours", path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := listen(dir); err == nil {
		t.Fatal("symlink socket accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()
	l, lock, err := listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	defer l.Close()
	if err := checkSocket(path, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := listen(dir); err == nil {
		t.Fatal("accepted second instance")
	}
}

func TestIPCBadFrameDoesNotAffectOtherClients(t *testing.T) {
	c, _ := startTestRun(t, Options{RuntimeDir: shortRuntime(t), StartQuick: func(context.Context, string, string, bool, func(st.Preview), func(string)) (st.Quick, error) {
		t.Error("invalid request reached capture")
		return nil, errors.New("unexpected")
	}})
	dir, err := runtimeDir(c.RuntimeDir, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, socketName)
	slow, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	if _, err := slow.Write([]byte(`{"version":`)); err != nil {
		t.Fatal(err)
	}
	bad, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if err := bad.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Write([]byte("{\"version\":1,\"action\":\"dictate.start\",\"unexpected\":true}\n")); err != nil {
		t.Fatal(err)
	}
	var r st.Reply
	if err := readFrame(bufio.NewReader(bad), &r); err != nil || r.Error != string(ErrRequest) {
		t.Fatal(r, err)
	}
	if r, err := c.Do(t.Context(), st.Request{Version: st.Version, Action: "status"}); err != nil || r.Status.Capture != "idle" {
		t.Fatal(r, err)
	}
	if err := readFrame(bufio.NewReader(strings.NewReader(strings.Repeat(" ", st.MaxFrame)+"\n")), &r); !errors.Is(err, errFrameSize) {
		t.Fatal(err)
	}
}

func TestIPCRejectsForeignRuntimeOwner(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test needs a runtime directory owned by a different UID")
	}
	for _, dir := range []string{"/root", "/private/var/root"} {
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0700 || owner(fi) == uint32(os.Geteuid()) {
			continue
		}
		if _, err := runtimeDir(dir, false); err == nil || !strings.Contains(err.Error(), "owned by this user") {
			t.Fatal(err)
		}
		return
	}
	t.Skip("no accessible private directory owned by a different UID")
}
