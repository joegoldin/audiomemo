package transcribe

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

type nemoAuth struct {
	key  string
	dial func(context.Context, string, string) (net.Conn, error)
}

func (a nemoAuth) client() *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: a.dial}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Check the server end of THIS established connection, not merely the current
// listener on its port. No request bytes leave before the child owns that end.
func childDial(pid int, addr string, done <-chan struct{}) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != addr {
			return nil, errors.New("unexpected local model endpoint")
		}
		probe, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		c, err := (&net.Dialer{}).DialContext(probe, "tcp4", addr)
		if err != nil {
			return nil, err
		}
		want := []byte("n" + addr + "->" + c.LocalAddr().String())
		ls := "lsof"
		if runtime.GOOS == "darwin" {
			ls = "/usr/sbin/lsof"
		}
		for {
			out, err := exec.CommandContext(probe, ls, "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:ESTABLISHED", "-Fn").Output()
			select {
			case <-done:
				c.Close()
				return nil, errors.New("local model exited")
			default:
			}
			if err == nil {
				for _, line := range bytes.Split(out, []byte{'\n'}) {
					if bytes.Equal(line, want) {
						return c, nil
					}
				}
			}
			select {
			case <-probe.Done():
				c.Close()
				return nil, errors.New("unable to verify the local model connection")
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
}
