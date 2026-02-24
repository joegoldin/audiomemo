package transcribe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDeepgramName(t *testing.T) {
	d := NewDeepgram("key", "nova-3")
	if d.Name() != "deepgram" {
		t.Errorf("expected 'deepgram', got %s", d.Name())
	}
}

func TestDeepgramNoAPIKey(t *testing.T) {
	d := NewDeepgram("", "nova-3")
	_, err := d.Transcribe(t.Context(), "test.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error with empty API key")
	}
}

func TestDeepgramParseResponse(t *testing.T) {
	resp := `{
		"metadata": {"duration": 5.0},
		"results": {
			"channels": [{
				"alternatives": [{
					"transcript": "Hello world",
					"confidence": 0.99
				}]
			}],
			"utterances": [
				{"start": 0.0, "end": 2.5, "transcript": "Hello"},
				{"start": 2.5, "end": 5.0, "transcript": "world"}
			]
		}
	}`

	d := NewDeepgram("key", "nova-3")
	result, err := d.parseResponse([]byte(resp), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result.Text)
	}
	if len(result.Segments) != 2 {
		t.Errorf("expected 2 segments, got %d", len(result.Segments))
	}
	if result.Duration != 5.0 {
		t.Errorf("expected duration 5.0, got %f", result.Duration)
	}
}

func TestDeepgramBuildQueryParams(t *testing.T) {
	d := NewDeepgram("key", "nova-3")
	params := d.buildQuery(TranscribeOpts{
		Language: "en", Model: "nova-2",
		SmartFormat: true, Punctuate: true, Diarize: true,
		FillerWords: true, Numerals: true,
	})
	if params.Get("model") != "nova-2" {
		t.Errorf("expected nova-2, got %s", params.Get("model"))
	}
	if params.Get("language") != "en" {
		t.Errorf("expected en, got %s", params.Get("language"))
	}
	for _, param := range []string{"smart_format", "diarize", "punctuate", "filler_words", "numerals"} {
		if params.Get(param) != "true" {
			t.Errorf("expected %s=true, got %q", param, params.Get(param))
		}
	}
}

func TestDeepgramBuildQueryParamsDisabled(t *testing.T) {
	d := NewDeepgram("key", "nova-3")
	params := d.buildQuery(TranscribeOpts{Language: "en", Model: "nova-2"})
	for _, param := range []string{"smart_format", "diarize", "punctuate", "filler_words", "numerals"} {
		if params.Get(param) != "" {
			t.Errorf("expected %s to be absent, got %q", param, params.Get(param))
		}
	}
}

func TestDeepgramRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-key" {
			t.Errorf("bad auth header: %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"duration": 1.0},
			"results": map[string]any{
				"channels": []any{map[string]any{
					"alternatives": []any{map[string]any{
						"transcript": "test",
					}},
				}},
			},
		})
	}))
	defer server.Close()

	d := NewDeepgram("test-key", "nova-3")
	d.baseURL = server.URL

	// Create a temp audio file
	tmp := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(tmp, []byte("fake audio"), 0644)

	result, err := d.Transcribe(t.Context(), tmp, TranscribeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "test" {
		t.Errorf("expected 'test', got %q", result.Text)
	}
}
