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

func TestMistralName(t *testing.T) {
	m := NewMistral("key", "voxtral-mini-latest")
	if m.Name() != "mistral" {
		t.Errorf("expected 'mistral', got %s", m.Name())
	}
}

func TestMistralNoAPIKey(t *testing.T) {
	m := NewMistral("", "voxtral-mini-latest")
	_, err := m.Transcribe(t.Context(), "test.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error with empty API key")
	}
}

func TestMistralParseResponse(t *testing.T) {
	resp := `{
		"model": "voxtral-mini-latest",
		"text": "Hello world",
		"language": "en",
		"segments": [
			{"start": 0.0, "end": 1.5, "text": "Hello"},
			{"start": 1.5, "end": 3.0, "text": "world"}
		]
	}`
	m := NewMistral("key", "voxtral-mini-latest")
	result, err := m.parseResponse([]byte(resp))
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

func TestMistralRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("bad auth header: %s", r.Header.Get("Authorization"))
		}
		ct := r.Header.Get("Content-Type")
		if !strings.Contains(ct, "multipart/form-data") {
			t.Errorf("expected multipart, got %s", ct)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"text":     "bonjour",
			"language": "fr",
			"model":    "voxtral-mini-latest",
		})
	}))
	defer server.Close()

	m := NewMistral("test-key", "voxtral-mini-latest")
	m.baseURL = server.URL

	tmp := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(tmp, []byte("fake"), 0644)

	result, err := m.Transcribe(t.Context(), tmp, TranscribeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "bonjour" {
		t.Errorf("expected 'bonjour', got %q", result.Text)
	}
}
