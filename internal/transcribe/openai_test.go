package transcribe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAIName(t *testing.T) {
	o := NewOpenAI("key", "gpt-4o-transcribe")
	if o.Name() != "openai" {
		t.Errorf("expected 'openai', got %s", o.Name())
	}
}

func TestOpenAINoAPIKey(t *testing.T) {
	o := NewOpenAI("", "gpt-4o-transcribe")
	_, err := o.Transcribe(t.Context(), "test.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error with empty API key")
	}
}

func TestOpenAIParseVerboseResponse(t *testing.T) {
	resp := `{
		"text": "Hello world",
		"language": "en",
		"duration": 3.0,
		"segments": [
			{"start": 0.0, "end": 1.5, "text": "Hello"},
			{"start": 1.5, "end": 3.0, "text": "world"}
		]
	}`
	o := NewOpenAI("key", "gpt-4o-transcribe")
	result, err := o.parseVerboseResponse([]byte(resp))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result.Text)
	}
	if len(result.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(result.Segments))
	}
}

func TestOpenAIRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("bad auth header: %s", r.Header.Get("Authorization"))
		}
		ct := r.Header.Get("Content-Type")
		if !strings.Contains(ct, "multipart/form-data") {
			t.Errorf("expected multipart, got %s", ct)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"text":     "hello",
			"language": "en",
			"duration": 1.0,
		})
	}))
	defer server.Close()

	o := NewOpenAI("test-key", "gpt-4o-transcribe")
	o.baseURL = server.URL

	tmp := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(tmp, []byte("fake"), 0644)

	result, err := o.Transcribe(t.Context(), tmp, TranscribeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" {
		t.Errorf("expected 'hello', got %q", result.Text)
	}
}
