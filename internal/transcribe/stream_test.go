package transcribe

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// newTestStreamer returns a Streamer pointing at the given httptest server.
func newTestStreamer(server *httptest.Server) *Streamer {
	s := NewStreamer("test-key", false)
	s.baseURL = "ws://" + server.Listener.Addr().String()
	return s
}

// waitChan waits up to d for a value on ch and returns (value, true) or ("", false).
func waitChan(ch <-chan string, d time.Duration) (string, bool) {
	select {
	case v, ok := <-ch:
		return v, ok
	case <-time.After(d):
		return "", false
	}
}

func waitErr(ch <-chan error, d time.Duration) (error, bool) {
	select {
	case e, ok := <-ch:
		return e, ok
	case <-time.After(d):
		return nil, false
	}
}

// TestStreamerSessionStarted verifies that receiving a session_started message
// produces no error and Start returns nil.
func TestStreamerSessionStarted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		msg, _ := json.Marshal(map[string]string{"message_type": "session_started"})
		conn.WriteMessage(websocket.TextMessage, msg)

		// Keep the connection alive briefly
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	s := newTestStreamer(server)
	tmpFile := filepath.Join(t.TempDir(), "transcript.txt")
	pr, pw := io.Pipe()
	pw.Close() // no audio to send

	err := s.Start(t.Context(), pr, tmpFile)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer s.Stop()

	// No error should arrive
	select {
	case e := <-s.Err:
		t.Errorf("unexpected error: %v", e)
	case <-time.After(200 * time.Millisecond):
		// expected: no error
	}
}

// TestStreamerSendsAudioChunks verifies that audio data written to the pipe
// is received by the server as input_audio_chunk messages with correct base64 data.
func TestStreamerSendsAudioChunks(t *testing.T) {
	pcmData := []byte("hello pcm audio data bytes")

	received := make(chan audioChunkMsg, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg audioChunkMsg
			if err := json.Unmarshal(data, &msg); err == nil {
				received <- msg
			}
		}
	}))
	defer server.Close()

	s := newTestStreamer(server)
	tmpFile := filepath.Join(t.TempDir(), "transcript.txt")

	pr, pw := io.Pipe()
	err := s.Start(t.Context(), pr, tmpFile)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer s.Stop()

	// Write the PCM data then close
	pw.Write(pcmData)
	pw.Close()

	// Collect all received chunks and reconstruct
	var allDecoded []byte
	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg := <-received:
			if msg.MessageType != "input_audio_chunk" {
				t.Errorf("unexpected message_type: %s", msg.MessageType)
			}
			if msg.SampleRate != 16000 {
				t.Errorf("expected sample_rate 16000, got %d", msg.SampleRate)
			}
			decoded, err := base64.StdEncoding.DecodeString(msg.AudioBase64)
			if err != nil {
				t.Errorf("failed to decode base64: %v", err)
			}
			allDecoded = append(allDecoded, decoded...)
			if len(allDecoded) >= len(pcmData) {
				goto done
			}
		case <-timeout:
			t.Fatalf("timed out waiting for audio chunks; got %d bytes, want %d", len(allDecoded), len(pcmData))
		}
	}
done:
	if string(allDecoded) != string(pcmData) {
		t.Errorf("reconstructed audio mismatch: got %q, want %q", allDecoded, pcmData)
	}
}

// TestStreamerCommittedTranscript verifies committed_transcript messages arrive on Committed
// and are written to the transcript file.
func TestStreamerCommittedTranscript(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		msg, _ := json.Marshal(map[string]string{
			"message_type": "committed_transcript",
			"text":         "Hello world",
		})
		conn.WriteMessage(websocket.TextMessage, msg)
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	s := newTestStreamer(server)
	tmpFile := filepath.Join(t.TempDir(), "transcript.txt")
	pr, pw := io.Pipe()
	pw.Close()

	err := s.Start(t.Context(), pr, tmpFile)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer s.Stop()

	text, ok := waitChan(s.Committed, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for committed transcript")
	}
	if text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", text)
	}

	// Give file a moment to flush
	time.Sleep(50 * time.Millisecond)
	s.Stop()

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read transcript file: %v", err)
	}
	if !strings.Contains(string(data), "Hello world") {
		t.Errorf("transcript file missing expected text, got: %q", string(data))
	}
}

// TestStreamerPartialTranscript verifies partial_transcript messages arrive on Partial channel.
func TestStreamerPartialTranscript(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		msg, _ := json.Marshal(map[string]string{
			"message_type": "partial_transcript",
			"text":         "Hel",
		})
		conn.WriteMessage(websocket.TextMessage, msg)
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	s := newTestStreamer(server)
	tmpFile := filepath.Join(t.TempDir(), "transcript.txt")
	pr, pw := io.Pipe()
	pw.Close()

	err := s.Start(t.Context(), pr, tmpFile)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer s.Stop()

	text, ok := waitChan(s.Partial, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for partial transcript")
	}
	if text != "Hel" {
		t.Errorf("expected 'Hel', got %q", text)
	}
}

// TestStreamerIncrementalFileWrite sends multiple committed_transcript messages and verifies
// the file is updated after each one.
func TestStreamerIncrementalFileWrite(t *testing.T) {
	segments := []string{"First segment.", "Second segment.", "Third segment."}
	sendNext := make(chan struct{}, len(segments))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		for _, seg := range segments {
			<-sendNext
			msg, _ := json.Marshal(map[string]string{
				"message_type": "committed_transcript",
				"text":         seg,
			})
			conn.WriteMessage(websocket.TextMessage, msg)
		}
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	s := newTestStreamer(server)
	tmpFile := filepath.Join(t.TempDir(), "transcript.txt")
	pr, pw := io.Pipe()
	pw.Close()

	err := s.Start(t.Context(), pr, tmpFile)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer s.Stop()

	for i, seg := range segments {
		sendNext <- struct{}{}
		text, ok := waitChan(s.Committed, 2*time.Second)
		if !ok {
			t.Fatalf("timed out waiting for segment %d", i+1)
		}
		if text != seg {
			t.Errorf("segment %d: expected %q, got %q", i+1, seg, text)
		}

		// Flush should have already happened; give a tiny moment
		time.Sleep(10 * time.Millisecond)
		data, err := os.ReadFile(tmpFile)
		if err != nil {
			t.Fatalf("failed to read transcript file after segment %d: %v", i+1, err)
		}
		if !strings.Contains(string(data), seg) {
			t.Errorf("file after segment %d missing %q; got: %q", i+1, seg, string(data))
		}
	}
}

// TestStreamerStop verifies that Stop() completes cleanly with no panics and closes channels.
func TestStreamerStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Just keep the connection open until client closes
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	s := newTestStreamer(server)
	tmpFile := filepath.Join(t.TempDir(), "transcript.txt")
	pr, pw := io.Pipe()
	defer pw.Close()

	err := s.Start(t.Context(), pr, tmpFile)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	// Stop should not panic and channels should be closed
	s.Stop()

	// Verify channels are closed (reads should return zero value + false)
	select {
	case _, ok := <-s.Committed:
		if ok {
			// might receive a value before close, that's fine
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Committed channel not closed after Stop()")
	}
}

// TestStreamerErrorHandling verifies that error message types from the server
// arrive on the Err channel.
func TestStreamerErrorHandling(t *testing.T) {
	for _, errType := range []string{"error", "auth_error", "quota_exceeded", "rate_limited"} {
		t.Run(errType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := wsUpgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade failed: %v", err)
					return
				}
				defer conn.Close()

				msg, _ := json.Marshal(map[string]string{
					"message_type": errType,
					"error":        "something went wrong",
				})
				conn.WriteMessage(websocket.TextMessage, msg)
				time.Sleep(200 * time.Millisecond)
			}))
			defer server.Close()

			s := newTestStreamer(server)
			tmpFile := filepath.Join(t.TempDir(), "transcript.txt")
			pr, pw := io.Pipe()
			pw.Close()

			err := s.Start(t.Context(), pr, tmpFile)
			if err != nil {
				t.Fatalf("Start returned error: %v", err)
			}
			defer s.Stop()

			e, ok := waitErr(s.Err, 2*time.Second)
			if !ok {
				t.Fatalf("timed out waiting for error from %s", errType)
			}
			if e == nil {
				t.Error("expected non-nil error")
			}
			if !strings.Contains(e.Error(), errType) {
				t.Errorf("expected error to contain %q, got: %v", errType, e)
			}
			if !strings.Contains(e.Error(), "something went wrong") {
				t.Errorf("expected error to contain server-supplied detail, got: %v", e)
			}
		})
	}
}

// TestStreamerUsesRealtimeModel verifies the WebSocket URL contains the
// realtime model_id, not whatever batch model the user has configured.
func TestStreamerUsesRealtimeModel(t *testing.T) {
	got := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case got <- r.URL.RawQuery:
		default:
		}
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	s := newTestStreamer(server)
	tmpFile := filepath.Join(t.TempDir(), "transcript.txt")
	pr, pw := io.Pipe()
	pw.Close()
	if err := s.Start(t.Context(), pr, tmpFile); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer s.Stop()

	select {
	case q := <-got:
		if !strings.Contains(q, "model_id=scribe_v2_realtime") {
			t.Errorf("expected query to contain model_id=scribe_v2_realtime, got: %s", q)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive a request")
	}
}

// TestStreamerDrainsPipeAfterSendFailure verifies that once the WebSocket
// closes mid-stream the sendLoop keeps reading from the PCM pipe. This is
// critical: if it stopped, ffmpeg's PCM writes would block and stall the
// entire recording pipeline.
func TestStreamerDrainsPipeAfterSendFailure(t *testing.T) {
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(connected)
		// Close immediately so the next client write fails.
		conn.Close()
	}))
	defer server.Close()

	s := newTestStreamer(server)
	tmpFile := filepath.Join(t.TempDir(), "transcript.txt")
	pr, pw := io.Pipe()
	if err := s.Start(t.Context(), pr, tmpFile); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer s.Stop()

	<-connected

	// Push a chunk so the first write triggers the failure path.
	go func() { pw.Write(make([]byte, 4096)) }()

	// Then push enough additional data to overflow any reasonable in-process
	// buffer. If sendLoop stopped reading after the failure, this Write would
	// block forever; we'd time out below.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 64; i++ {
			if _, err := pw.Write(make([]byte, 4096)); err != nil {
				return
			}
		}
		close(done)
	}()

	select {
	case <-done:
		// Pipe was drained successfully.
	case <-time.After(2 * time.Second):
		t.Fatal("sendLoop did not drain pipe after WS failure; ffmpeg would block here")
	}
	pw.Close()
}
