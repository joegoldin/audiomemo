//go:build linux || darwin

package desktopservice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
	"golang.org/x/sys/unix"
)

const (
	socketName     = "control.sock"
	ioTimeout      = 5 * time.Second
	controlTimeout = 30 * time.Second
	waitTimeout    = 24 * time.Hour
	maxConnections = 32
	maxWaiters     = 8
)

// Run serves one foreground daemon. Desktop.Run, when provided, is invoked
// directly on the caller's thread; the adapter owns any native thread setup.
func Run(ctx context.Context, opts Options) error {
	if opts.StartQuick == nil && ((opts.StartMeeting == nil && opts.StartMeetingPreview == nil) || opts.FinalizeMeeting == nil) {
		return errors.New("desktop capture backends are not configured")
	}
	listener, lock, err := listen(opts.RuntimeDir)
	if err != nil {
		return err
	}
	defer lock.Close() // Keep the instance lock until socket unlink and shutdown.
	c := newController(ctx, opts)
	served := make(chan error, 1)
	go func() {
		err := serve(listener, c)
		c.cancel()
		served <- err
	}()
	closeListener := context.AfterFunc(c.ctx, func() { listener.Close() })
	defer closeListener()
	var desktopErr error
	if opts.Desktop != nil {
		c.changedLocked() // publish initial idle status
		go c.desktopUpdates()
		desktopErr = opts.Desktop.Run(c.ctx, c)
		if desktopErr == nil && c.ctx.Err() == nil {
			desktopErr = errors.New("desktop adapter stopped before daemon shutdown")
		}
		c.cancel()
	} else {
		<-c.ctx.Done()
	}
	listener.Close()
	return errors.Join(c.shutdown(), <-served, desktopErr)
}

// Client makes a single versioned request, using the same private runtime
// directory as Run by default. Cancellation only closes this connection.
type Client struct{ RuntimeDir string }

func (c Client) Do(ctx context.Context, req st.Request) (st.Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout(req))
	defer cancel()
	reply, err := c.exchange(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return reply, ctx.Err()
		}
		return reply, fmt.Errorf("desktop daemon unavailable: %w", err)
	}
	if reply.Version != st.Version {
		return reply, &ReplyError{ErrVersion, "unsupported daemon reply version"}
	}
	return reply, ErrorFromReply(reply)
}

func (c Client) exchange(ctx context.Context, req st.Request) (reply st.Reply, err error) {
	root, err := runtimeDir(c.RuntimeDir, false)
	if err != nil {
		return reply, err
	}
	path := filepath.Join(root, socketName)
	if err = checkSocket(path, true); err != nil {
		return reply, err
	}
	dialer := net.Dialer{Timeout: ioTimeout}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return reply, err
	}
	defer conn.Close()
	uc := conn.(*net.UnixConn)
	if err = checkPeer(uc); err != nil {
		return reply, err
	}
	closeConn := context.AfterFunc(ctx, func() { conn.Close() })
	defer closeConn()
	if err = conn.SetWriteDeadline(time.Now().Add(ioTimeout)); err != nil {
		return reply, err
	}
	if err = writeFrame(conn, req); err != nil {
		return reply, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err = conn.SetReadDeadline(deadline); err != nil {
			return reply, err
		}
	}
	err = readFrame(bufio.NewReader(conn), &reply)
	return reply, err
}

func requestTimeout(req st.Request) time.Duration {
	if req.Action == "meeting.stop" && (req.Wait || req.Print == "text") {
		return waitTimeout
	}
	return controlTimeout
}

func runtimeDir(override string, create bool) (string, error) {
	if override == "" {
		override = os.Getenv("AUDIOMEMO_RUNTIME_DIR")
	}
	if override == "" {
		override = filepath.Join(defaultRuntimeParent(), "audiomemo-"+strconv.Itoa(os.Geteuid()))
	}
	if !filepath.IsAbs(override) {
		return "", errors.New("desktop runtime directory must be absolute")
	}
	override = filepath.Clean(override)
	if filepath.Dir(override) == override {
		return "", errors.New("desktop runtime directory cannot be the filesystem root")
	}
	// Darwin's /var is a system symlink. Use the resolved parent consistently,
	// then enforce a chain that no other non-root user can redirect or replace.
	parent, err := filepath.EvalSymlinks(filepath.Dir(override))
	if err != nil {
		return "", err
	}
	if err = checkAncestors(parent); err != nil {
		return "", err
	}
	root := filepath.Join(parent, filepath.Base(override))
	if create {
		if err = os.Mkdir(root, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	fi, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() || fi.Mode().Perm() != 0700 || owner(fi) != uint32(os.Geteuid()) {
		return "", fmt.Errorf("desktop runtime directory %q must be owned by this user and have mode 0700", root)
	}
	return root, nil
}

func checkAncestors(path string) error {
	for {
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		uid := owner(fi)
		stickyRoot := uid == 0 && fi.Mode()&os.ModeSticky != 0
		if !fi.IsDir() || (uid != 0 && uid != uint32(os.Geteuid())) || (fi.Mode().Perm()&0022 != 0 && !stickyRoot) {
			return fmt.Errorf("untrusted desktop runtime parent %q", path)
		}
		parent := filepath.Dir(path)
		if path == parent {
			return nil
		}
		path = parent
	}
}

func checkSocket(path string, private bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 || (private && fi.Mode().Perm() != 0600) || owner(fi) != uint32(os.Geteuid()) {
		return errors.New("desktop control socket must be owned by this user and have mode 0600")
	}
	return nil
}

func instanceLock(root string) (*os.File, error) {
	path := filepath.Join(root, "daemon.lock")
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err == nil {
		if stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Mode&(unix.S_IFMT|0777) != unix.S_IFREG|0600 {
			err = errors.New("desktop instance lock must be a private regular file")
		} else if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); errors.Is(err, unix.EWOULDBLOCK) {
			err = errors.New("desktop daemon is already running")
		}
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}

func removeStaleSocket(path string) error {
	if err := checkSocket(path, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	// flock covers cooperative instances. Don't remove a live socket even if
	// it wasn't created by a process holding this lock.
	conn, err := net.DialTimeout("unix", path, ioTimeout)
	if err == nil {
		conn.Close()
		return errors.New("desktop control socket is already in use")
	}
	if !errors.Is(err, unix.ECONNREFUSED) {
		return err
	}
	return os.Remove(path)
}

func listen(override string) (*net.UnixListener, *os.File, error) {
	root, err := runtimeDir(override, true)
	if err != nil {
		return nil, nil, err
	}
	lock, err := instanceLock(root)
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(root, socketName)
	if err = removeStaleSocket(path); err != nil {
		lock.Close()
		return nil, nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		lock.Close()
		return nil, nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		lock.Close()
		return nil, nil, err
	}
	return l, lock, nil
}

func checkPeer(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var uid uint32
	var peerErr error
	if err = raw.Control(func(fd uintptr) { uid, peerErr = peerUID(int(fd)) }); err != nil {
		return err
	}
	if peerErr != nil {
		return peerErr
	}
	if uid != uint32(os.Geteuid()) {
		return errors.New("desktop socket peer UID does not match this user")
	}
	return nil
}

func serve(l *net.UnixListener, c *controller) error {
	var workers sync.WaitGroup
	// On a listener failure, abort request waits before waiting for workers.
	// On graceful quit do not close sockets until they have sent their replies.
	defer workers.Wait()
	defer c.cancel()
	slots := make(chan struct{}, maxConnections)
	waits := make(chan struct{}, maxWaiters)
	for {
		conn, err := l.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
			workers.Go(func() {
				defer func() { <-slots }()
				defer conn.Close()
				serveConn(conn, c, waits)
			})
		default:
			conn.Close()
		}
	}
}

func serveConn(conn *net.UnixConn, c *controller, waits chan struct{}) {
	if checkPeer(conn) != nil {
		return
	}
	if conn.SetReadDeadline(time.Now().Add(ioTimeout)) != nil {
		return
	}
	reader := bufio.NewReader(conn)
	var r st.Request
	var reply st.Reply
	if err := readFrame(reader, &r); err != nil {
		reply = c.reply("", "", "", ErrRequest, "expected one bounded JSON request line")
	} else {
		allowed := true
		if requestTimeout(r) == waitTimeout {
			select {
			case waits <- struct{}{}:
				defer func() { <-waits }()
			default:
				allowed = false
			}
		}
		if allowed {
			reply = handleConn(conn, c, reader, r)
		} else {
			reply = c.reply("", "", "", ErrBusy, "too many waiting clients")
		}
	}
	if conn.SetWriteDeadline(time.Now().Add(ioTimeout)) != nil {
		return
	}
	if err := writeFrame(conn, reply); errors.Is(err, errFrameSize) {
		writeFrame(conn, c.reply(reply.ID, reply.Path, "", ErrFinalize, "reply exceeds control frame limit; use the meeting bundle"))
	}
}

func handleConn(conn *net.UnixConn, c *controller, reader *bufio.Reader, r st.Request) st.Reply {
	ctx, cancel := context.WithTimeout(c.ctx, requestTimeout(r))
	defer cancel()
	if conn.SetReadDeadline(time.Time{}) != nil {
		return c.reply("", "", "", ErrUnavailable, "could not set client deadline")
	}
	// A connection carries exactly one request. EOF or additional input
	// abandons the wait; an already admitted action remains daemon-owned.
	go func() { reader.ReadByte(); cancel() }()
	return c.Do(ctx, r)
}

var errFrameSize = errors.New("control frame exceeds size limit")

func readFrame(r *bufio.Reader, dst any) error {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > st.MaxFrame {
			return errFrameSize
		}
		line = append(line, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

func writeFrame(w io.Writer, value any) error {
	frame, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(frame) >= st.MaxFrame {
		return errFrameSize
	}
	n, err := w.Write(append(frame, '\n'))
	if err == nil && n != len(frame)+1 {
		return io.ErrShortWrite
	}
	return err
}
