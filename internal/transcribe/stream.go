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

	"github.com/gorilla/websocket"
)

// Streamer manages a realtime ElevenLabs WebSocket transcription session.
type Streamer struct {
	apiKey       string
	model        string
	storeInCloud bool
	baseURL      string // "wss://api.elevenlabs.io" default, overridable for tests

	Committed chan string // finalized text segments
	Partial   chan string // in-progress text (replaced on each update)
	Err       chan error  // fatal errors

	conn   *websocket.Conn
	cancel context.CancelFunc
	mu     sync.Mutex

	committed []string      // accumulated committed text
	file      *os.File      // transcript file, flushed on each commit
	writer    *bufio.Writer

	once sync.Once
}

// NewStreamer allocates a new Streamer with buffered channels.
func NewStreamer(apiKey, model string, storeInCloud bool) *Streamer {
	return &Streamer{
		apiKey:       apiKey,
		model:        model,
		storeInCloud: storeInCloud,
		baseURL:      "wss://api.elevenlabs.io",
		Committed:    make(chan string, 64),
		Partial:      make(chan string, 16),
		Err:          make(chan error, 1),
	}
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
	Message     string `json:"message"` // used in error messages
}

// Start dials the ElevenLabs WebSocket endpoint and begins streaming audio from pcmReader.
// It opens transcriptPath for appending committed transcripts.
// Returns nil after successfully connecting and spawning background goroutines.
func (s *Streamer) Start(ctx context.Context, pcmReader io.Reader, transcriptPath string) error {
	wsURL := fmt.Sprintf(
		"%s/v1/speech-to-text/realtime?model_id=%s&commit_strategy=vad&vad_silence_threshold_secs=1&audio_format=pcm_16000",
		s.baseURL, s.model,
	)

	headers := http.Header{}
	headers.Set("xi-api-key", s.apiKey)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
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
	go s.recvLoop(derived)

	return nil
}

func (s *Streamer) sendLoop(ctx context.Context, r io.Reader) {
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
			data, jerr := json.Marshal(msg)
			if jerr != nil {
				return
			}
			if werr := s.conn.WriteMessage(websocket.TextMessage, data); werr != nil {
				return
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

func (s *Streamer) recvLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, data, err := s.conn.ReadMessage()
		if err != nil {
			// If context was cancelled or connection was closed normally, don't report error.
			if ctx.Err() != nil {
				return
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			// Abnormal EOF from server shutdown is not worth alarming about.
			if websocket.IsUnexpectedCloseError(err) {
				return
			}
			select {
			case s.Err <- fmt.Errorf("websocket read error: %w", err):
			default:
			}
			return
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

		case "error", "auth_error", "quota_exceeded", "rate_limited":
			errMsg := msg.Message
			if errMsg == "" {
				errMsg = msg.Text
			}
			if errMsg == "" {
				errMsg = msg.MessageType
			}
			select {
			case s.Err <- fmt.Errorf("elevenlabs error (%s): %s", msg.MessageType, errMsg):
			default:
			}
			return
		}
	}
}

// Stop cancels the session, closes the WebSocket connection, flushes and closes the file,
// and closes the output channels.
func (s *Streamer) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		s.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		s.conn.Close()
	}
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
