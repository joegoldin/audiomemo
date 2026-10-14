package transcribe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
)

// Keep the model process out of the terminal's signal group. Recording uses
// Ctrl+C to finalize audio; it must not also kill the preview before flushing.
func isolateCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
}

func startNemoServer(ctx context.Context, cfg config.NemoConfig) (string, string, func(), error) {
	base, key, cleanup, err := serveNemo(ctx, cfg, cfg.LiveModel, "local preview", "--asr.endpointing.enable=true")
	if err != nil {
		return "", "", nil, err
	}
	return "ws" + strings.TrimPrefix(base, "http") + "/v1/audio/transcriptions/realtime", key, cleanup, nil
}

// serveNemo starts a private nemo-speech server with the given ASR model and
// waits until it is ready. It returns the server's http base URL and bearer
// key. role names the server in errors, since a preview and a final pass can
// fail at the same step for different reasons.
func serveNemo(ctx context.Context, cfg config.NemoConfig, model, role string, extraArgs ...string) (string, string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", "", nil, err
	}
	key := hex.EncodeToString(keyBytes)
	processCtx, cancel := context.WithCancel(ctx)
	args := append([]string{"serve", "--asr-model", model,
		"--device", cfg.Device, "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--no-ui"}, extraArgs...)
	cmd := exec.CommandContext(processCtx, cfg.Binary, args...)
	isolateCommand(cmd)
	// The random bearer token prevents a port-allocation race from sending audio
	// to another listener. Do not place it in process arguments or diagnostics.
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "NEMO_SPEECH_HTTP_API_KEY=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "NEMO_SPEECH_HTTP_API_KEY="+key)
	var logs tailBuffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		cancel()
		return "", "", nil, fmt.Errorf("start %s: %w", role, err)
	}
	done := make(chan struct{})
	var exitErr error
	go func() { exitErr = cmd.Wait(); close(done) }()
	var once sync.Once
	cleanup := func() { once.Do(func() { cancel(); <-done }) }
	seconds := cfg.StartupTimeout
	if seconds <= 0 {
		seconds = 120
	}
	startup, stop := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer stop()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-startup.Done():
			cleanup()
			return "", "", nil, fmt.Errorf("%s startup: %w (pre-download models with nemo-speech pull %s)", role, startup.Err(), model)
		case <-done:
			cleanup()
			return "", "", nil, fmt.Errorf("%s exited: %v: %s", role, exitErr, strings.ReplaceAll(strings.TrimSpace(logs.String()), key, "[redacted]"))
		case <-ticker.C:
			req, err := http.NewRequestWithContext(startup, "GET", base+"/ready", nil)
			if err != nil {
				cleanup()
				return "", "", nil, err
			}
			response, err := client.Do(req)
			if err != nil {
				continue
			}
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return base, key, cleanup, nil
			}
		}
	}
}
