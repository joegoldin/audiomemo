package transcribe

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Result struct {
	Words        []Word        `json:"words,omitempty"`
	SpeakerTurns []SpeakerTurn `json:"speaker_turns,omitempty"`
	Text         string        `json:"text"`
	Segments     []Segment     `json:"segments,omitempty"`
	Language     string        `json:"language,omitempty"`
	Duration     float64       `json:"duration,omitempty"`
}

type Word struct {
	Word    string  `json:"word"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
}

// SpeakerTurns preserve overlapping activity; word attribution selects the
// speaker with the most temporal overlap, not a claim to separate mixed voices.
type SpeakerTurn struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
}

type Segment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker string  `json:"speaker,omitempty"`
}

func (r *Result) Format(f OutputFormat) string {
	switch f {
	case FormatJSON:
		return r.formatJSON()
	case FormatSRT:
		return r.formatSRT()
	case FormatVTT:
		return r.formatVTT()
	default:
		return r.formatText()
	}
}

func (r *Result) formatText() string {
	segs := r.segments()
	if !r.hasSpeakers(segs) {
		return r.Text
	}
	var b strings.Builder
	for i, seg := range segs {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s: %s", displaySpeaker(seg.Speaker), strings.TrimSpace(seg.Text))
	}
	return b.String()
}

func (r *Result) formatJSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

func (r *Result) hasSpeakers(segs []Segment) bool {
	if len(r.SpeakerTurns) > 0 {
		return true
	}
	for _, seg := range segs {
		if seg.Speaker != "" {
			return true
		}
	}
	return false
}

func displaySpeaker(speaker string) string {
	if speaker == "" {
		return "unassigned"
	}
	return speaker
}

func (r *Result) segments() []Segment {
	if len(r.Segments) > 0 {
		return r.Segments
	}
	return []Segment{{Start: 0, End: r.Duration, Text: r.Text}}
}

func (r *Result) formatSRT() string {
	var b strings.Builder
	segs := r.segments()
	hasSpeaker := r.hasSpeakers(segs)
	for i, seg := range segs {
		fmt.Fprintf(&b, "%d\n", i+1)
		fmt.Fprintf(&b, "%s --> %s\n", srtTime(seg.Start), srtTime(seg.End))
		text := strings.TrimSpace(seg.Text)
		if hasSpeaker {
			text = fmt.Sprintf("[%s] %s", displaySpeaker(seg.Speaker), text)
		}
		fmt.Fprintf(&b, "%s\n\n", text)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func (r *Result) formatVTT() string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	segs := r.segments()
	hasSpeaker := r.hasSpeakers(segs)
	for _, seg := range segs {
		fmt.Fprintf(&b, "%s --> %s\n", vttTime(seg.Start), vttTime(seg.End))
		text := strings.TrimSpace(seg.Text)
		if hasSpeaker {
			text = fmt.Sprintf("[%s] %s", displaySpeaker(seg.Speaker), text)
		}
		fmt.Fprintf(&b, "%s\n\n", text)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func srtTime(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	ms := int((seconds - float64(int(seconds))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func vttTime(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	ms := int((seconds - float64(int(seconds))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
