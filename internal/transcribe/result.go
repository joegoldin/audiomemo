package transcribe

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Result struct {
	Text     string    `json:"text"`
	Segments []Segment `json:"segments,omitempty"`
	Language string    `json:"language,omitempty"`
	Duration float64   `json:"duration,omitempty"`
}

type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
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
		return r.Text
	}
}

func (r *Result) formatJSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

func (r *Result) segments() []Segment {
	if len(r.Segments) > 0 {
		return r.Segments
	}
	return []Segment{{Start: 0, End: r.Duration, Text: r.Text}}
}

func (r *Result) formatSRT() string {
	var b strings.Builder
	for i, seg := range r.segments() {
		fmt.Fprintf(&b, "%d\n", i+1)
		fmt.Fprintf(&b, "%s --> %s\n", srtTime(seg.Start), srtTime(seg.End))
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(seg.Text))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func (r *Result) formatVTT() string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, seg := range r.segments() {
		fmt.Fprintf(&b, "%s --> %s\n", vttTime(seg.Start), vttTime(seg.End))
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(seg.Text))
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
