package transcribe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestElevenLabsName(t *testing.T) {
	e := NewElevenLabs("key", "scribe_v2", false)
	if e.Name() != "elevenlabs" {
		t.Errorf("expected 'elevenlabs', got %s", e.Name())
	}
}

func TestElevenLabsNoAPIKey(t *testing.T) {
	e := NewElevenLabs("", "scribe_v2", false)
	_, err := e.Transcribe(t.Context(), "test.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error with empty API key")
	}
}

func TestElevenLabsUnsupportedOpts(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(tmp, []byte("fake"), 0644)

	for _, tc := range []struct {
		name string
		opts TranscribeOpts
	}{
		{"smart-format", TranscribeOpts{SmartFormat: true}},
		{"punctuate", TranscribeOpts{Punctuate: true}},
		{"filler-words", TranscribeOpts{FillerWords: true}},
		{"numerals", TranscribeOpts{Numerals: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Use a server that returns 200 so we know validation catches it first
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
			}))
			defer server.Close()
			e2 := NewElevenLabs("key", "scribe_v2", false)
			e2.baseURL = server.URL
			_, err := e2.Transcribe(t.Context(), tmp, tc.opts)
			if err == nil {
				t.Errorf("expected error for unsupported option %s", tc.name)
			}
		})
	}
}

func TestElevenLabsParseResponse(t *testing.T) {
	dur := 5.0
	tid := "txn_123"
	resp := elevenlabsResponse{
		LanguageCode:        "eng",
		LanguageProbability: 0.99,
		Text:                "Hello world",
		TranscriptionID:     &tid,
		AudioDurationSecs:   &dur,
		Words: []elevenlabsWord{
			{Text: "Hello", Start: ptr(0.0), End: ptr(0.5), Type: "word"},
			{Text: " ", Type: "spacing"},
			{Text: "world", Start: ptr(0.6), End: ptr(1.0), Type: "word"},
		},
	}

	data, _ := json.Marshal(resp)
	e := NewElevenLabs("key", "scribe_v2", false)
	result, transcriptionID, err := e.parseResponse(data, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result.Text)
	}
	if result.Language != "eng" {
		t.Errorf("expected 'eng', got %q", result.Language)
	}
	if result.Duration != 5.0 {
		t.Errorf("expected duration 5.0, got %f", result.Duration)
	}
	if transcriptionID != "txn_123" {
		t.Errorf("expected 'txn_123', got %q", transcriptionID)
	}
}

func TestElevenLabsParseResponseDiarize(t *testing.T) {
	sp0 := "0"
	sp1 := "1"
	resp := elevenlabsResponse{
		LanguageCode: "eng",
		Text:         "Hello there How are you",
		Words: []elevenlabsWord{
			{Text: "Hello", Start: ptr(0.0), End: ptr(0.5), Type: "word", SpeakerID: &sp0},
			{Text: " ", Type: "spacing", SpeakerID: &sp0},
			{Text: "there", Start: ptr(0.6), End: ptr(1.0), Type: "word", SpeakerID: &sp0},
			{Text: " ", Type: "spacing", SpeakerID: &sp1},
			{Text: "How", Start: ptr(1.5), End: ptr(1.8), Type: "word", SpeakerID: &sp1},
			{Text: " ", Type: "spacing", SpeakerID: &sp1},
			{Text: "are", Start: ptr(1.9), End: ptr(2.0), Type: "word", SpeakerID: &sp1},
			{Text: " ", Type: "spacing", SpeakerID: &sp1},
			{Text: "you", Start: ptr(2.1), End: ptr(2.5), Type: "word", SpeakerID: &sp1},
		},
	}

	data, _ := json.Marshal(resp)
	e := NewElevenLabs("key", "scribe_v2", false)
	result, _, err := e.parseResponse(data, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(result.Segments))
	}
	if result.Segments[0].Speaker != "Speaker 0" {
		t.Errorf("expected 'Speaker 0', got %q", result.Segments[0].Speaker)
	}
	if result.Segments[1].Speaker != "Speaker 1" {
		t.Errorf("expected 'Speaker 1', got %q", result.Segments[1].Speaker)
	}
}

func TestElevenLabsRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "test-key" {
			t.Errorf("bad auth header: %s", r.Header.Get("xi-api-key"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"language_code":        "eng",
			"language_probability": 0.99,
			"text":                 "test transcript",
			"words":               []any{},
		})
	}))
	defer server.Close()

	e := NewElevenLabs("test-key", "scribe_v2", false)
	e.baseURL = server.URL

	tmp := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(tmp, []byte("fake audio"), 0644)

	result, err := e.Transcribe(t.Context(), tmp, TranscribeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "test transcript" {
		t.Errorf("expected 'test transcript', got %q", result.Text)
	}
}

func TestElevenLabsDeleteAfterTranscribe(t *testing.T) {
	var deleteCalled atomic.Int32
	tid := "txn_delete_me"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleteCalled.Add(1)
			if r.URL.Path != "/v1/speech-to-text/transcripts/txn_delete_me" {
				t.Errorf("unexpected delete path: %s", r.URL.Path)
			}
			w.WriteHeader(200)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"language_code":        "eng",
			"language_probability": 0.99,
			"text":                 "test",
			"words":               []any{},
			"transcription_id":    tid,
		})
	}))
	defer server.Close()

	tmp := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(tmp, []byte("fake audio"), 0644)

	// storeInCloud=false: should delete
	e := NewElevenLabs("test-key", "scribe_v2", false)
	e.baseURL = server.URL
	_, err := e.Transcribe(t.Context(), tmp, TranscribeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if deleteCalled.Load() != 1 {
		t.Errorf("expected delete to be called once, got %d", deleteCalled.Load())
	}

	// storeInCloud=true: should NOT delete
	deleteCalled.Store(0)
	e2 := NewElevenLabs("test-key", "scribe_v2", true)
	e2.baseURL = server.URL
	_, err = e2.Transcribe(t.Context(), tmp, TranscribeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if deleteCalled.Load() != 0 {
		t.Errorf("expected no delete call, got %d", deleteCalled.Load())
	}
}

func ptr(f float64) *float64 { return &f }
