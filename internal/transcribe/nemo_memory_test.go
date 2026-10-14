package transcribe

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joegoldin/audiomemo/internal/config"
)

func TestWAVHeader(t *testing.T) {
	h := WAVHeader(32000, 16000, 1)
	if len(h) != 44 || string(h[0:4]) != "RIFF" || string(h[8:16]) != "WAVEfmt " || string(h[36:40]) != "data" {
		t.Fatalf("not a RIFF/WAVE header: %q", h)
	}
	for _, f := range []struct {
		name      string
		got, want uint32
	}{
		{"riff size", binary.LittleEndian.Uint32(h[4:]), 36 + 32000},
		{"format", uint32(binary.LittleEndian.Uint16(h[20:])), 1},
		{"channels", uint32(binary.LittleEndian.Uint16(h[22:])), 1},
		{"sample rate", binary.LittleEndian.Uint32(h[24:]), 16000},
		{"byte rate", binary.LittleEndian.Uint32(h[28:]), 32000},
		{"bits", uint32(binary.LittleEndian.Uint16(h[34:])), 16},
		{"data size", binary.LittleEndian.Uint32(h[40:]), 32000},
	} {
		if f.got != f.want {
			t.Errorf("%s = %d, want %d", f.name, f.got, f.want)
		}
	}
}

func TestNemoTranscribePCMUploadsFromMemory(t *testing.T) {
	pcm := bytes.Repeat([]byte{1, 2}, 800)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request %s with auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("no file part: %v", err)
		}
		defer f.Close()
		got, _ := io.ReadAll(f)
		if !bytes.Equal(got, append(WAVHeader(len(pcm), 16000, 1), pcm...)) {
			t.Errorf("upload is not the PCM wrapped in a WAV header (%d bytes)", len(got))
		}
		if r.FormValue("response_format") != "json" {
			t.Errorf("response_format = %q", r.FormValue("response_format"))
		}
		w.Write([]byte(`{"text":" spoken words "}`))
	}))
	defer server.Close()

	var servedModel string
	n := NewNemo(config.NemoConfig{Binary: "sh", Model: "parakeet-tdt", LiveModel: "nemotron-en"})
	n.serve = func(_ context.Context, _ config.NemoConfig, model, _ string, _ ...string) (string, string, func(), error) {
		servedModel = model
		return server.URL, "secret", func() {}, nil
	}
	result, err := n.TranscribePCM(t.Context(), pcm, 16000, TranscribeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "spoken words" {
		t.Errorf("text = %q", result.Text)
	}
	if servedModel != "parakeet-tdt" {
		t.Errorf("final pass loaded %q, want the batch model", servedModel)
	}
}

func TestNemoTranscribePCMRefusesDiarize(t *testing.T) {
	n := NewNemo(config.NemoConfig{Binary: "sh"})
	n.serve = func(context.Context, config.NemoConfig, string, string, ...string) (string, string, func(), error) {
		t.Fatal("must not start a server for an unsupported request")
		return "", "", nil, nil
	}
	_, err := n.TranscribePCM(t.Context(), []byte{0, 0}, 16000, TranscribeOpts{Diarize: true})
	if err == nil || !strings.Contains(err.Error(), "diarize") {
		t.Errorf("err = %v, want a diarize refusal", err)
	}
}
