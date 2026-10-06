package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/joegoldin/audiomemo/internal/config"
)

// Nemo runs ASR and diarization in separate processes so their model allocations
// do not accumulate. The live server is stopped before this final pass starts.
type Nemo struct {
	config config.NemoConfig
}

func NewNemo(cfg config.NemoConfig) *Nemo { return &Nemo{config: cfg} }
func (n *Nemo) Name() string              { return "nemo" }

func (n *Nemo) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if err := validateOpts(n.Name(), opts, true, false, false, false, false); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath(n.config.Binary); err != nil {
		return nil, fmt.Errorf("NeMo-Speech.cpp binary %q unavailable: %w (install nemo-speech with ASR, diarization and HTTP support)", n.config.Binary, err)
	}
	if _, err := os.Stat(audioPath); err != nil {
		return nil, fmt.Errorf("audio file: %w", err)
	}
	tmp, err := os.MkdirTemp("", "audiomemo-nemo-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	// Normalize even WAV input: its extension does not guarantee PCM16 audio.
	wav, err := convertToWav(ctx, audioPath, tmp, opts.Verbose)
	if err != nil {
		return nil, fmt.Errorf("prepare NeMo audio: %w", err)
	}
	model := n.config.Model
	if opts.Model != "" {
		model = opts.Model
	}
	args := []string{"transcribe", wav, "--model", model, "--device", n.config.Device, "--format", "json"}
	if opts.Language != "" {
		args = append(args, "--language", opts.Language)
	}
	data, err := runNemo(ctx, n.config.Binary, args, opts.Verbose)
	if err != nil {
		return nil, err
	}
	var out struct {
		Text      *string  `json:"text"`
		Duration  float64  `json:"duration"`
		Languages []string `json:"languages"`
		Words     []Word   `json:"words"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode NeMo ASR: %w", err)
	}
	if out.Text == nil {
		return nil, fmt.Errorf("NeMo ASR output is missing text")
	}
	result := &Result{Text: strings.TrimSpace(*out.Text), Duration: out.Duration, Words: out.Words}
	if len(out.Languages) > 0 {
		result.Language = out.Languages[0]
	}
	if result.Text != "" && len(result.Words) == 0 {
		return nil, fmt.Errorf("NeMo ASR returned text without word timestamps")
	}
	for _, word := range result.Words {
		if word.Start < 0 || word.End < word.Start {
			return nil, fmt.Errorf("NeMo ASR returned invalid word timestamps")
		}
	}
	if opts.Diarize && result.Text != "" {
		data, err = runNemo(ctx, n.config.Binary, []string{"diarize", wav, "--model", n.config.DiarModel, "--device", n.config.Device, "--preset", "v3-offline", "--format", "json"}, opts.Verbose)
		if err != nil {
			return nil, err
		}
		var diar struct {
			Segments *[]struct {
				Start   float64 `json:"start"`
				End     float64 `json:"end"`
				Speaker int     `json:"speaker"`
			} `json:"segments"`
		}
		if err := json.Unmarshal(data, &diar); err != nil {
			return nil, fmt.Errorf("decode NeMo diarization: %w", err)
		}
		if diar.Segments == nil || len(*diar.Segments) == 0 {
			return nil, fmt.Errorf("NeMo diarization returned no speaker turns for speech")
		}
		for _, turn := range *diar.Segments {
			if turn.Start < 0 || turn.End < turn.Start || turn.Speaker < 1 {
				return nil, fmt.Errorf("NeMo diarization returned an invalid speaker turn")
			}
			result.SpeakerTurns = append(result.SpeakerTurns, SpeakerTurn{Start: turn.Start, End: turn.End, Speaker: fmt.Sprintf("speaker_%d", turn.Speaker)})
		}
		assignSpeakers(result)
	}
	result.Segments = segmentWords(result.Words)
	return result, nil
}

// tailBuffer bounds subprocess diagnostics, including long model download logs.
type tailBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > 8192 {
		b.data = b.data[len(b.data)-8192:]
	}
	return len(p), nil
}
func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func runNemo(ctx context.Context, binary string, args []string, verbose bool) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	isolateCommand(cmd)
	var output bytes.Buffer
	var stderr tailBuffer
	cmd.Stdout = &output
	cmd.Stderr = &stderr
	if verbose {
		cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("nemo-speech %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return output.Bytes(), nil
}

func assignSpeakers(result *Result) {
	turns := result.SpeakerTurns
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].Start < turns[j].Start })
	first := 0
	for i := range result.Words {
		word := &result.Words[i]
		for first < len(turns) && turns[first].End < word.Start {
			first++
		}
		best := 0.0
		for j := first; j < len(turns) && turns[j].Start <= word.End; j++ {
			turn := turns[j]
			overlap := min(word.End, turn.End) - max(word.Start, turn.Start)
			if overlap > best || (word.Start == word.End && word.Start >= turn.Start && word.Start <= turn.End && word.Speaker == "") {
				best, word.Speaker = overlap, turn.Speaker
			}
		}
		// Punctuation can have zero duration outside a diarizer's speech bounds.
		if word.Speaker == "" && i > 0 && punctuationOnly(word.Word) {
			word.Speaker = result.Words[i-1].Speaker
		}
	}
}

func punctuationOnly(s string) bool {
	return strings.TrimSpace(s) != "" && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPunct(r) && !unicode.IsSpace(r) }) == -1
}

func segmentWords(words []Word) []Segment {
	var segments []Segment
	for _, word := range words {
		text := strings.TrimSpace(word.Word)
		if text == "" {
			continue
		}
		if len(segments) > 0 {
			last := &segments[len(segments)-1]
			if last.Speaker == word.Speaker && word.Start-last.End < 1 && word.End-last.Start <= 8 && !strings.HasSuffix(last.Text, ".") && !strings.HasSuffix(last.Text, "?") && !strings.HasSuffix(last.Text, "!") {
				if !punctuationOnly(text) {
					last.Text += " "
				}
				last.Text += text
				last.End = max(last.End, word.End)
				continue
			}
		}
		segments = append(segments, Segment{Start: word.Start, End: word.End, Text: text, Speaker: word.Speaker})
	}
	return segments
}
