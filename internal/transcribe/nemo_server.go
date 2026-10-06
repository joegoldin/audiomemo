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
	cmd := exec.CommandContext(processCtx, cfg.Binary, "serve", "--asr-model", cfg.LiveModel,
		"--device", cfg.Device, "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--no-ui",
		"--asr.endpointing.enable=true")
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
		return "", "", nil, fmt.Errorf("start local preview: %w", err)
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
			return "", "", nil, fmt.Errorf("local preview startup: %w (pre-download models with nemo-speech pull %s)", startup.Err(), cfg.LiveModel)
		case <-done:
			cleanup()
			return "", "", nil, fmt.Errorf("local preview exited: %v: %s", exitErr, strings.ReplaceAll(strings.TrimSpace(logs.String()), key, "[redacted]"))
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
				return "ws://127.0.0.1:" + strconv.Itoa(port) + "/v1/audio/transcriptions/realtime", key, cleanup, nil
			}
		}
	}
}
