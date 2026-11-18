package meetingfinal

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/joegoldin/audiomemo/internal/transcribe"
)

const attributionNotice = "Us/Them identifies the source, not the person. Loudspeaker bleed or overlapping speech can make attribution ambiguous; headphones reduce bleed. Speaker labels are local to a track and recognition window."

type Span struct {
	TrackID string  `json:"track_id"`
	Role    string  `json:"role"`
	Speaker string  `json:"speaker,omitempty"`
	Window  int     `json:"window"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}
type Word struct {
	Span
	Text string `json:"word"`
}
type Excerpt struct {
	Span
	Text string `json:"text"`
}
type Untimed struct {
	TrackID string `json:"track_id"`
	Role    string `json:"role"`
	Window  int    `json:"window"`
	Text    string `json:"text"`
}
type Transcript struct {
	Version  int       `json:"version"`
	Notice   string    `json:"attribution_notice"`
	Words    []Word    `json:"words"`
	Turns    []Span    `json:"speaker_turns"`
	Excerpts []Excerpt `json:"excerpts"`
	Untimed  []Untimed `json:"untimed,omitempty"`
}

func validTime(start, end, duration float64) bool {
	return !math.IsNaN(start) && !math.IsNaN(end) && start >= 0 && end > start && start < duration && !math.IsInf(end, 1)
}

func annotate(out *Transcript, result *transcribe.Result, track sessiontypes.Track, window int, from, to int64) {
	offset, duration := float64(from)/1000, float64(to-from)/1000
	span := func(start, end float64, speaker string) Span {
		return Span{TrackID: track.ID, Role: track.Role, Window: window, Start: offset + start, End: offset + min(end, duration), Speaker: speaker}
	}
	var turns []transcribe.SpeakerTurn
	for _, turn := range result.SpeakerTurns {
		if validTime(turn.Start, turn.End, duration) {
			turns = append(turns, turn)
			out.Turns = append(out.Turns, span(turn.Start, turn.End, turn.Speaker))
		}
	}
	slices.SortStableFunc(turns, func(a, b transcribe.SpeakerTurn) int { return cmp.Compare(a.Start, b.Start) })
	// Only use a speaker when the timeline supports exactly one active label.
	// In particular, NeMo's best-overlap label is not proof during double-talk.
	speaker := func(start, end float64) string {
		name := ""
		covered, seen := 0.0, start
		for _, turn := range turns {
			if turn.Start >= end {
				break
			}
			if turn.End <= start || turn.Speaker == "" {
				continue
			}
			if name != "" && name != turn.Speaker {
				return ""
			}
			name = turn.Speaker
			finish := min(end, turn.End)
			covered += max(0, finish-max(seen, turn.Start))
			seen = max(seen, finish)
		}
		if covered < (end-start)/2 {
			return ""
		}
		return name
	}
	var words []Word
	var untimed []string
	for _, w := range result.Words {
		if validTime(w.Start, w.End, duration) {
			words = append(words, Word{Span: span(w.Start, w.End, speaker(w.Start, min(w.End, duration))), Text: w.Word})
		} else if text := strings.TrimSpace(w.Word); text != "" {
			untimed = append(untimed, text)
		}
	}
	slices.SortStableFunc(words, func(a, b Word) int { return compareSpan(a.Span, b.Span) })
	out.Words = append(out.Words, words...)
	if len(words) > 0 {
		out.Excerpts = append(out.Excerpts, group(words)...)
	} else if len(result.Words) == 0 {
		for _, seg := range result.Segments {
			if validTime(seg.Start, seg.End, duration) && strings.TrimSpace(seg.Text) != "" {
				out.Excerpts = append(out.Excerpts, Excerpt{Span: span(seg.Start, seg.End, speaker(seg.Start, min(seg.End, duration))), Text: strings.TrimSpace(seg.Text)})
			} else if text := strings.TrimSpace(seg.Text); text != "" {
				untimed = append(untimed, text)
			}
		}
		if len(result.Segments) == 0 && strings.TrimSpace(result.Text) != "" {
			untimed = append(untimed, strings.TrimSpace(result.Text))
		}
	}
	if len(untimed) > 0 {
		out.Untimed = append(out.Untimed, Untimed{TrackID: track.ID, Role: track.Role, Window: window, Text: strings.Join(untimed, " ")})
	}
}

func group(words []Word) []Excerpt {
	var out []Excerpt
	for start := 0; start < len(words); {
		first := words[start]
		end := start + 1
		for end < len(words) && end-start < 20 && words[end].Speaker == first.Speaker && words[end].Start-words[end-1].End <= 0.8 && words[end].End-first.Start <= 8 {
			end++
		}
		var text strings.Builder
		s := first.Span
		for _, w := range words[start:end] {
			piece := strings.TrimSpace(w.Text)
			if text.Len() > 0 && piece != "" && !strings.ContainsAny(piece[:1], ".,!?;:") {
				text.WriteByte(' ')
			}
			text.WriteString(piece)
			s.End = max(s.End, w.End)
		}
		if text.Len() > 0 {
			out = append(out, Excerpt{Span: s, Text: text.String()})
		}
		start = end
	}
	return out
}

func compareSpan(a, b Span) int {
	return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(a.TrackID, b.TrackID), cmp.Compare(a.Window, b.Window), cmp.Compare(a.End, b.End), cmp.Compare(a.Speaker, b.Speaker))
}

func (t *Transcript) sort() {
	slices.SortStableFunc(t.Words, func(a, b Word) int { return compareSpan(a.Span, b.Span) })
	slices.SortStableFunc(t.Turns, compareSpan)
	slices.SortStableFunc(t.Excerpts, func(a, b Excerpt) int { return compareSpan(a.Span, b.Span) })
}
func label(role, track, speaker string, window int) string {
	name := "Them"
	if role == "us" {
		name = "Us"
	}
	if speaker != "" {
		return fmt.Sprintf("%s / %s:%d:%s", name, track, window, speaker)
	}
	return name + " / " + track
}
func (t *Transcript) text() string {
	var b strings.Builder
	for _, seg := range t.Excerpts {
		fmt.Fprintf(&b, "%s: %s\n", label(seg.Role, seg.TrackID, seg.Speaker, seg.Window), seg.Text)
	}
	for _, line := range t.Untimed {
		fmt.Fprintf(&b, "%s (untimed): %s\n", label(line.Role, line.TrackID, "", line.Window), line.Text)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
func (t *Transcript) subtitle(format transcribe.OutputFormat) string {
	if len(t.Excerpts) == 0 {
		if format == transcribe.FormatVTT {
			return "WEBVTT\n\n"
		}
		return ""
	}
	var r transcribe.Result
	for _, seg := range t.Excerpts {
		r.Segments = append(r.Segments, transcribe.Segment{Start: seg.Start, End: seg.End, Text: seg.Text, Speaker: label(seg.Role, seg.TrackID, seg.Speaker, seg.Window)})
	}
	return r.Format(format)
}
