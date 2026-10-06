package transcribe

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joegoldin/audiomemo/internal/config"
)

const realtimeModelID = "scribe_v2_realtime"
const RealtimeBackendName = "elevenlabs"
const (
	defaultReconnectBackoff = time.Second
	maxReconnectBackoff     = 30 * time.Second
)

// Streamer owns a preview session. Auto mode tries a private local server first,
// then ElevenLabs if configured. The saved recording is always refined separately.
type Streamer struct {
	apiKey           string
	reconnectBackoff time.Duration
	backend          string
	storeInCloud     bool
	baseURL          string
	local            *config.NemoConfig
	language         string
	managed          bool
	fallback         bool

	Committed chan string
	Partial   chan string
	Err       chan error
	Warning   chan error

	cancel    context.CancelFunc
	done      chan struct{}
	pumpDone  chan struct{}
	reader    io.Reader
	mu        sync.Mutex
	committed []string
	writer    *bufio.Writer
	file      *os.File
	once      sync.Once

	// Tests can substitute the managed process without loading models.
	startLocal func(context.Context, config.NemoConfig) (string, string, func(), error)
}

func NewStreamer(apiKey string, storeInCloud bool) *Streamer {
	return &Streamer{
		apiKey: apiKey, storeInCloud: storeInCloud,
		reconnectBackoff: defaultReconnectBackoff, backend: RealtimeBackendName,
		baseURL:   "wss://api.elevenlabs.io",
		Committed: make(chan string, 64), Partial: make(chan string, 16),
		Err: make(chan error, 1), Warning: make(chan error, 8),
		startLocal: startNemoServer,
	}
}

// Name reports the preview provider, including a switch after local failure.
func (s *Streamer) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend
}

// NewLiveStreamer honors explicit backends. Unsupported live backends get batch
// transcription only; selecting nemo/local never authorizes a cloud upload.
func NewLiveStreamer(cfg *config.Config, backend string) (*Streamer, error) {
	if backend == "" {
		backend = cfg.Transcribe.DefaultBackend
	}
	s := NewStreamer(cfg.Transcribe.ElevenLabs.APIKey, cfg.Transcribe.ElevenLabs.StoreInCloud)
	s.language = cfg.Transcribe.Language
	s.managed = true
	switch backend {
	case "", "auto", "nemo", "local":
		local := cfg.Transcribe.Nemo
		s.local = &local
		s.backend = "nemo"
		s.fallback = (backend == "" || backend == "auto") && s.apiKey != ""
	case "elevenlabs":
		if s.apiKey == "" {
			return nil, fmt.Errorf("elevenlabs API key not configured")
		}
	case "whisper", "whisper-cpp", "whisperx", "ffmpeg-whisper", "deepgram", "openai", "mistral":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown backend: %s", backend)
	}
	return s, nil
}

type audioChunkMsg struct {
	MessageType string `json:"message_type"`
	AudioBase64 string `json:"audio_base_64"`
	Commit      bool   `json:"commit"`
	SampleRate  int    `json:"sample_rate"`
}

type wsIncomingMsg struct {
	MessageType string          `json:"message_type"`
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	Delta       string          `json:"delta"`
	Transcript  string          `json:"transcript"`
	Message     string          `json:"message"`
	Error       json.RawMessage `json:"error"`
}

// Start drains PCM immediately, even while models load or after preview failure,
// so a slow or broken preview cannot block ffmpeg's recording pipe. Managed
// startup is asynchronous; errors and fallback notices arrive on the channels.
// A blocking reader must implement io.Closer so Stop can interrupt it.
func (s *Streamer) Start(ctx context.Context, pcmReader io.Reader, transcriptPath string) error {
	f, err := os.OpenFile(transcriptPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open preview transcript: %w", err)
	}
	derived, cancel := context.WithCancel(ctx)
	s.cancel, s.reader, s.file, s.writer = cancel, pcmReader, f, bufio.NewWriter(f)
	s.done = make(chan struct{})
	var initial *websocket.Conn
	if !s.managed {
		initial, err = s.dialCloud(derived)
		if err != nil {
			cancel()
			f.Close()
			close(s.done)
			return err
		}
	}
	packets := make(chan []byte, 256)
	s.pumpDone = make(chan struct{})
	go func() { defer close(s.pumpDone); s.pump(derived, pcmReader, packets) }()
	go func() {
		defer close(s.done)
		if initial != nil {
			err = s.superviseCloud(derived, initial, packets)
		} else {
			err = s.run(derived, packets)
		}
		if err != nil && derived.Err() == nil {
			s.report(s.Err, err)
		}
	}()
	return nil
}

func (s *Streamer) pump(ctx context.Context, reader io.Reader, packets chan []byte) {
	defer close(packets)
	warned := false
	for {
		buf := make([]byte, 4096)
		n, err := io.ReadFull(reader, buf)
		if err == io.ErrUnexpectedEOF {
			err = io.EOF
		}
		if n > 0 {
			select {
			case <-ctx.Done():
				return
			case packets <- buf[:n]:
			default:
				if !warned {
					s.report(s.Warning, fmt.Errorf("live preview is behind; some preview audio was dropped (saved recording is unaffected)"))
					warned = true
				}
			}
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				s.report(s.Warning, fmt.Errorf("preview PCM: %w", err))
			}
			return
		}
	}
}

func (s *Streamer) run(ctx context.Context, packets <-chan []byte) error {
	if s.local != nil {
		endpoint, key, cleanup, err := s.startLocal(ctx, *s.local)
		if err == nil {
			headers := http.Header{"Authorization": []string{"Bearer " + key}}
			dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
			conn, response, dialErr := dialer.DialContext(ctx, endpoint, headers)
			if response != nil {
				response.Body.Close()
			}
			err = dialErr
			if err == nil {
				err = s.session(ctx, conn, true, packets)
			}
			cleanup()
		}
		if err == nil || ctx.Err() != nil {
			return err
		}
		if !s.fallback {
			return fmt.Errorf("local live preview: %w", err)
		}
		s.report(s.Warning, fmt.Errorf("local live preview failed: %w; falling back to ElevenLabs (subsequent audio will be uploaded)", err))
		s.partial("")
	}
	s.mu.Lock()
	s.backend = RealtimeBackendName
	s.mu.Unlock()
	conn, err := s.dialCloud(ctx)
	if err != nil {
		return err
	}
	return s.superviseCloud(ctx, conn, packets)
}

func (s *Streamer) dialCloud(ctx context.Context) (*websocket.Conn, error) {
	query := url.Values{"model_id": {realtimeModelID}, "commit_strategy": {"vad"}, "vad_silence_threshold_secs": {"1"}, "audio_format": {"pcm_16000"}}
	if s.language != "" {
		query.Set("language_code", s.language)
	}
	query.Set("enable_logging", strconv.FormatBool(s.storeInCloud))
	headers := http.Header{"xi-api-key": []string{s.apiKey}}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, response, err := dialer.DialContext(ctx, s.baseURL+"/v1/speech-to-text/realtime?"+query.Encode(), headers)
	if response != nil {
		response.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("elevenlabs websocket dial: %w", err)
	}
	return conn, nil
}

func (s *Streamer) session(ctx context.Context, conn *websocket.Conn, local bool, packets <-chan []byte) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); conn.Close(); wg.Wait() }()
	conn.SetReadLimit(4 << 20)
	if local {
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := conn.WriteJSON(map[string]any{"type": "session.update", "session": map[string]any{"sample_rate": 16000, "language": s.language}}); err != nil {
			return err
		}
	}
	type received struct {
		msg wsIncomingMsg
		err error
	}
	events := make(chan received, 16)
	sent := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			var msg wsIncomingMsg
			err := conn.ReadJSON(&msg)
			select {
			case events <- received{msg, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-packets:
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				var err error
				if local {
					if ok {
						err = conn.WriteMessage(websocket.BinaryMessage, data)
					} else {
						err = conn.WriteJSON(map[string]string{"type": "input_audio_buffer.commit"})
					}
				} else {
					err = conn.WriteJSON(audioChunkMsg{MessageType: "input_audio_chunk", AudioBase64: base64.StdEncoding.EncodeToString(data), Commit: !ok, SampleRate: 16000})
				}
				if err != nil || !ok {
					sent <- err
					return
				}
			}
		}
	}()
	var partial string
	var flush <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sent:
			if err != nil {
				return err
			}
			timer = time.NewTimer(3 * time.Second)
			flush = timer.C
		case <-flush:
			if local {
				return fmt.Errorf("local preview did not acknowledge final audio")
			}
			return nil
		case event := <-events:
			if event.err != nil {
				if flush != nil && !local {
					return nil
				}
				return fmt.Errorf("live websocket read: %w", event.err)
			}
			msg := event.msg
			switch {
			case msg.Type == "input_audio_buffer.committed":
				return nil
			case msg.Type == "conversation.item.input_audio_transcription.delta":
				partial += msg.Delta
				s.partial(partial)
			case msg.MessageType == "partial_transcript":
				s.partial(msg.Text)
			case msg.Type == "conversation.item.input_audio_transcription.completed" || msg.MessageType == "committed_transcript" || msg.MessageType == "committed_transcript_with_timestamps":
				text := msg.Text
				if local {
					text = msg.Transcript
				}
				if err := s.commit(text); err != nil {
					return err
				}
				partial = ""
				s.partial("")
			case msg.Type == "error" || isErrorMessageType(msg.MessageType):
				message := msg.Message
				if message == "" {
					var detail struct {
						Message string `json:"message"`
					}
					if json.Unmarshal(msg.Error, &message) != nil {
						_ = json.Unmarshal(msg.Error, &detail)
						message = detail.Message
					}
				}
				if message == "" {
					message = msg.Text
				}
				if !local {
					return &scribeError{msgType: msg.MessageType, detail: message}
				}
				return fmt.Errorf("live transcription error (%s): %s", msg.Type, message)
			}
		}
	}
}

func (s *Streamer) report(ch chan error, err error) {
	select {
	case ch <- err:
	default:
	}
}
func (s *Streamer) partial(text string) {
	select {
	case s.Partial <- text:
	default:
	}
}
func (s *Streamer) commit(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.committed = append(s.committed, text)
	if _, err := fmt.Fprintln(s.writer, text); err != nil {
		return err
	}
	if err := s.writer.Flush(); err != nil {
		return err
	}
	select {
	case s.Committed <- text:
	default:
	}
	return nil
}

// Stop gives end-of-recording results a short flush window, then cancels and
// reaps the local model process before the final ASR model can be loaded.
func (s *Streamer) Stop() {
	s.once.Do(func() {
		if s.done != nil {
			select {
			case <-s.done:
			case <-time.After(4 * time.Second):
			}
		}
		if s.cancel != nil {
			s.cancel()
		}
		if closer, ok := s.reader.(io.Closer); ok {
			closer.Close()
		}
		if s.done != nil {
			<-s.done
		}
		if s.pumpDone != nil {
			<-s.pumpDone
		}
		s.mu.Lock()
		if s.writer != nil {
			s.writer.Flush()
		}
		if s.file != nil {
			s.file.Close()
		}
		s.mu.Unlock()
		close(s.Committed)
		close(s.Partial)
		close(s.Err)
		close(s.Warning)
	})
}

func (s *Streamer) FullText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.committed, " ")
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

// superviseCloud retains the realtime reconnect policy across session expiry.
// The independent PCM pump keeps recording unblocked throughout backoff.
func (s *Streamer) superviseCloud(ctx context.Context, conn *websocket.Conn, packets <-chan []byte) error {
	for {
		err := s.session(ctx, conn, false, packets)
		if err == nil || ctx.Err() != nil {
			return err
		}
		if se, ok := err.(*scribeError); ok && isFatalScribeError(se.msgType) {
			return err
		}
		backoff := s.reconnectBackoff
		if backoff <= 0 {
			backoff = defaultReconnectBackoff
		}
		for attempts := 1; ; attempts++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			conn, err = s.dialCloud(ctx)
			if err == nil {
				break
			}
			if attempts >= 5 {
				return fmt.Errorf("elevenlabs reconnect failed after %d attempts: %w", attempts, err)
			}
			backoff = min(backoff*2, maxReconnectBackoff)
		}
	}
}
