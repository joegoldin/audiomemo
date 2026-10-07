package transcribe

import (
	"strings"
	"testing"
)

func TestResultFormatText(t *testing.T) {
	r := &Result{Text: "Hello world", Language: "en", Duration: 2.5}
	out := r.Format(FormatText)
	if out != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", out)
	}
}

func TestResultFormatSRT(t *testing.T) {
	r := &Result{
		Text: "Hello world",
		Segments: []Segment{
			{Start: 0.0, End: 1.5, Text: "Hello"},
			{Start: 1.5, End: 2.5, Text: "world"},
		},
	}
	out := r.Format(FormatSRT)
	if !strings.Contains(out, "00:00:00,000 --> 00:00:01,500") {
		t.Errorf("expected SRT timestamp, got:\n%s", out)
	}
	if !strings.Contains(out, "1\n") {
		t.Errorf("expected sequence number, got:\n%s", out)
	}
}

func TestResultFormatVTT(t *testing.T) {
	r := &Result{
		Text: "Hello world",
		Segments: []Segment{
			{Start: 0.0, End: 1.5, Text: "Hello"},
		},
	}
	out := r.Format(FormatVTT)
	if !strings.HasPrefix(out, "WEBVTT\n") {
		t.Errorf("expected WEBVTT header, got:\n%s", out)
	}
	if !strings.Contains(out, "00:00:00.000 --> 00:00:01.500") {
		t.Errorf("expected VTT timestamp, got:\n%s", out)
	}
}

func TestResultFormatJSON(t *testing.T) {
	r := &Result{Text: "Hello", Language: "en", Duration: 1.0}
	out := r.Format(FormatJSON)
	if !strings.Contains(out, `"text"`) {
		t.Errorf("expected JSON with text field, got:\n%s", out)
	}
}

func TestResultFormatTextFallsBackWhenNoSegments(t *testing.T) {
	r := &Result{Text: "Hello world"}
	out := r.Format(FormatSRT)
	// With no segments, SRT should create one segment from full text
	if !strings.Contains(out, "Hello world") {
		t.Errorf("expected fallback text, got:\n%s", out)
	}
}

func TestDiarizedOutputMarksUnassignedSpeech(t *testing.T) {
	r := &Result{
		Text:     "First. Uncertain.",
		Segments: []Segment{{Start: 0, End: 1, Text: "First.", Speaker: "speaker_1"}, {Start: 2, End: 3, Text: "Uncertain."}},
	}
	for _, format := range []OutputFormat{FormatText, FormatSRT, FormatVTT} {
		output := r.Format(format)
		if !strings.Contains(output, "unassigned") {
			t.Errorf("%s must not silently imply the previous speaker continues: %s", format, output)
		}
	}
	if strings.Contains(r.Format(FormatJSON), "unassigned") {
		t.Fatal("display labels must not become invented JSON speaker identities")
	}
	// A diarizer can return speech turns that cover none of the ASR's words.
	r.Segments = []Segment{{Start: 2, End: 3, Text: "Uncertain."}}
	r.SpeakerTurns = []SpeakerTurn{{Start: 0, End: 1, Speaker: "speaker_1"}}
	if !strings.Contains(r.Format(FormatText), "unassigned") {
		t.Fatal("fully unassigned diarized output must be explicit")
	}
	// Plain, non-diarized transcription remains plain text.
	r.SpeakerTurns = nil
	if strings.Contains(r.Format(FormatText), "unassigned") {
		t.Fatal("non-diarized output must not acquire speaker labels")
	}
}
