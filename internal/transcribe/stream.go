package transcribe

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// realtimeModelID is the only ElevenLabs model that supports the realtime
// WebSocket endpoint. The batch "scribe_v2" model is not valid here.
const realtimeModelID = "scribe_v2_realtime"

// defaultReconnectBackoff is the initial delay between reconnect attempts
// after a non-fatal session failure. Doubles up to maxReconnectBackoff.
const (
	defaultReconnectBackoff = time.Second
	maxReconnectBackoff     = 30 * time.Second
)

// Streamer manages a realtime ElevenLabs WebSocket transcription session.
// On non-fatal session failures (e.g. session_time_limit_exceeded after ~1h
// of streaming) it automatically dials a new WebSocket and continues
// delivering transcripts on the same channels.
type Streamer struct {
	apiKey           string
	storeInCloud     bool
	baseURL          string // "wss://api.elevenlabs.io" default, overridable for tests
	reconnectBackoff time.Duration

	Committed chan string // finalized text segments
	Partial   chan string // in-progress text (replaced on each update)
	Err       chan error  // fatal errors (auth, quota, etc. — not reconnectable)

	connMu sync.RWMutex
	conn   *websocket.Conn // nil while a reconnect is in flight

	cancel context.CancelFunc
	mu     sync.Mutex

	committed []string // accumulated committed text
	file      *os.File // transcript file, flushed on each commit
	writer    *bufio.Writer

	once sync.Once
}

// NewStreamer allocates a new Streamer with buffered channels.
func NewStreamer(apiKey string, storeInCloud bool) *Streamer {
	return &Streamer{
		apiKey:           apiKey,
		storeInCloud:     storeInCloud,
		baseURL:          "wss://api.elevenlabs.io",
		reconnectBackoff: defaultReconnectBackoff,
		Committed:        make(chan string, 64),
		Partial:          make(chan string, 16),
		Err:              make(chan error, 1),
	}
}

// errorMessageTypes lists every realtime API message_type that signals a
// session-fatal error per the ElevenLabs docs.
var errorMessageTypes = map[string]struct{}{
	"error":                       {},
	"auth_error":                  {},
	"quota_exceeded":              {},
	"commit_throttled":            {},
	"unaccepted_terms":            {},
	"rate_limited":                {},
	"queue_overflow":              {},
	"resource_exhausted":          {},
	"session_time_limit_exceeded": {},
	"input_error":                 {},
	"chunk_size_exceeded":         {},
	"insufficient_audio_activity": {},
	"transcriber_error":           {},
}

func isErrorMessageType(t string) bool {
	_, ok := errorMessageTypes[t]
	return ok
}

// fatalErrorMessageTypes lists errors that will never recover by reconnecting.
// All other error types in errorMessageTypes trigger a reconnect attempt.
var fatalErrorMessageTypes = map[string]struct{}{
	"auth_error":          {},
	"quota_exceeded":      {},
	"unaccepted_terms":    {},
	"input_error":         {},
	"chunk_size_exceeded": {},
}

func isFatalScribeError(t string) bool {
	_, ok := fatalErrorMessageTypes[t]
	return ok
}

type audioChunkMsg struct {
	MessageType string `json:"message_type"`
	AudioBase64 string `json:"audio_base_64"`
	Commit      bool   `json:"commit"`
	SampleRate  int    `json:"sample_rate"`
}

type wsIncomingMsg struct {
	MessageType string `json:"message_type"`
	Text        string `json:"text"`
	Error       string `json:"error"` // populated on error message types
}

// scribeError is returned from recvLoop when the server sends an error
// message. The supervisor uses isFatalScribeError to decide whether to
// reconnect or surface the error.
type scribeError struct {
	msgType string
	detail  string
}

func (e *scribeError) Error() string {
	return fmt.Sprintf("elevenlabs error (%s): %s", e.msgType, e.detail)
}

// Start dials the ElevenLabs WebSocket endpoint and begins streaming audio
// from pcmReader. It opens transcriptPath for appending committed transcripts.
// Returns nil after successfully connecting and spawning background goroutines.
// Subsequent disconnects are handled by an internal supervisor that reconnects
// automatically; only the initial dial failure is reported by Start.
func (s *Streamer) Start(ctx context.Context, pcmReader io.Reader, transcriptPath string) error {
	conn, err := s.dial()
	if err != nil {
		return fmt.Errorf("elevenlabs websocket dial failed: %w", err)
	}
	s.conn = conn

	f, err := os.OpenFile(transcriptPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to open transcript file: %w", err)
	}
	s.file = f
	s.writer = bufio.NewWriter(f)

	derived, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	go s.sendLoop(derived, pcmReader)
	go s.supervise(derived)

	return nil
}

func (s *Streamer) dial() (*websocket.Conn, error) {
	enableLogging := "true"
	if !s.storeInCloud {
		enableLogging = "false"
	}
	wsURL := fmt.Sprintf(
		"%s/v1/speech-to-text/realtime?model_id=%s&commit_strategy=vad&vad_silence_threshold_secs=1&audio_format=pcm_16000&enable_logging=%s",
		s.baseURL, realtimeModelID, enableLogging,
	)
	headers := http.Header{}
	headers.Set("xi-api-key", s.apiKey)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	return conn, err
}

// supervise runs recvLoop in a loop, reconnecting on non-fatal session
// failures (notably session_time_limit_exceeded after ~1h). Exits on context
// cancel or a fatal scribe error.
func (s *Streamer) supervise(ctx context.Context) {
	backoff := s.reconnectBackoff
	if backoff <= 0 {
		backoff = defaultReconnectBackoff
	}
	for {
		err := s.recvLoop(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			return
		}
		if se, ok := err.(*scribeError); ok && isFatalScribeError(se.msgType) {
			select {
			case s.Err <- se:
			default:
			}
			return
		}

		// Tear down the dead conn so sendLoop drops chunks until we have a
		// new one.
		s.connMu.Lock()
		if s.conn != nil {
			s.conn.Close()
			s.conn = nil
		}
		s.connMu.Unlock()

		// Reconnect with exponential backoff. Give up if it keeps failing.
		attempts := 0
		current := backoff
		for {
			select {
			case <-time.After(current):
			case <-ctx.Done():
				return
			}
			newConn, derr := s.dial()
			if derr == nil {
				s.connMu.Lock()
				s.conn = newConn
				s.connMu.Unlock()
				break
			}
			attempts++
			if attempts >= 5 {
				select {
				case s.Err <- fmt.Errorf("elevenlabs reconnect failed after %d attempts: %w", attempts, derr):
				default:
				}
				return
			}
			if current < maxReconnectBackoff {
				current *= 2
			}
		}
	}
}

func (s *Streamer) sendLoop(ctx context.Context, r io.Reader) {
	// We MUST keep reading from the pipe even while disconnected. Otherwise
	// ffmpeg backpressures on pipe writes and stalls the entire recording
	// pipeline. Chunks sent during a reconnect window are dropped — the
	// post-recording batch transcribe fills any gaps.
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := r.Read(buf)
		if n > 0 {
			encoded := base64.StdEncoding.EncodeToString(buf[:n])
			msg := audioChunkMsg{
				MessageType: "input_audio_chunk",
				AudioBase64: encoded,
				Commit:      false,
				SampleRate:  16000,
			}
			if data, jerr := json.Marshal(msg); jerr == nil {
				s.connMu.RLock()
				conn := s.conn
				s.connMu.RUnlock()
				if conn != nil {
					_ = conn.WriteMessage(websocket.TextMessage, data)
				}
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
	}
}

// recvLoop reads from the current connection until it dies or returns an
// error message. Returns nil on clean shutdown, an error otherwise. The
// supervisor decides whether to reconnect based on the error.
func (s *Streamer) recvLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		s.connMu.RLock()
		conn := s.conn
		s.connMu.RUnlock()
		if conn == nil {
			return fmt.Errorf("connection closed before read")
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// Any network-level disconnect (normal, going away, unexpected,
			// etc.) returns an error so the supervisor can reconnect.
			return err
		}

		var msg wsIncomingMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.MessageType {
		case "session_started":
			// ignore

		case "partial_transcript":
			select {
			case s.Partial <- msg.Text:
			default:
			}

		case "committed_transcript":
			s.mu.Lock()
			s.committed = append(s.committed, msg.Text)
			if s.writer != nil {
				fmt.Fprintln(s.writer, msg.Text)
				s.writer.Flush()
			}
			s.mu.Unlock()
			select {
			case s.Committed <- msg.Text:
			default:
			}

		default:
			if isErrorMessageType(msg.MessageType) {
				detail := msg.Error
				if detail == "" {
					detail = msg.Text
				}
				if detail == "" {
					detail = msg.MessageType
				}
				return &scribeError{msgType: msg.MessageType, detail: detail}
			}
		}
	}
}

// Stop cancels the session, closes the WebSocket connection, flushes and
// closes the file, and closes the output channels.
func (s *Streamer) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.connMu.Lock()
	if s.conn != nil {
		s.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		s.conn.Close()
		s.conn = nil
	}
	s.connMu.Unlock()

	s.mu.Lock()
	if s.writer != nil {
		s.writer.Flush()
	}
	if s.file != nil {
		s.file.Close()
		s.file = nil
	}
	s.mu.Unlock()

	s.once.Do(func() {
		close(s.Committed)
		close(s.Partial)
		close(s.Err)
	})
}

// FullText returns all committed segments joined by spaces.
func (s *Streamer) FullText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.committed, " ")
}
