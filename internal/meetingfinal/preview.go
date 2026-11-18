package meetingfinal

import (
	"context"
	"encoding/binary"
	"os"
	"strings"
	"time"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/joegoldin/audiomemo/internal/transcribe"
)

const draftSeconds = 4
const draftBytes = draftSeconds * recognitionRate * 2
const draftMaxTracks = 32
const draftMaxRunes = 240

type draftWindow struct {
	track string
	frame int64
	pcm   []byte
}

type draftResult struct {
	track, text string
	ok          bool
}

// Preview reads the single lossy capture feed and emits replacement draft
// snapshots. Keep one window per source, one active job and one pending job.
// Downstream emit must return promptly; canceling this context stops draft ASR.
func Preview(ctx context.Context, pcm <-chan sessiontypes.PCM, opts Options, emit func(sessiontypes.Preview)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	asr := opts.recognizer()
	result := make(chan draftResult, 1)
	byTrack := make(map[string]*draftWindow)
	busy := false
	var pending draftWindow
	behind := false
	stable := ""
	publish := func() {
		tentative := "Draft · final transcript will be rebuilt from the originals"
		if behind {
			tentative = "Draft behind · final transcript will be rebuilt from the originals"
		}
		if ctx.Err() == nil {
			emit(sessiontypes.Preview{Session: opts.Session, Stable: stable, Tentative: tentative})
		}
	}
	launch := func(window draftWindow) bool {
		select {
		case localModel <- struct{}{}:
			busy = true
			go func() {
				text, ok := draftASR(ctx, asr, opts, window.pcm)
				<-localModel
				result <- draftResult{track: window.track, text: text, ok: ok}
			}()
			return true
		default:
			return false
		}
	}
	publish()
	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-pcm:
			if !ok {
				return
			}
			if !validDraftPCM(p) {
				continue
			}
			d := byTrack[p.Track]
			if d == nil {
				if len(byTrack) >= draftMaxTracks {
					continue
				}
				d = &draftWindow{track: p.Track, pcm: make([]byte, 0, draftBytes)}
				byTrack[p.Track] = d
			}
			if d.append(p) && !behind {
				behind = true
				publish()
			}
			if len(d.pcm) == draftBytes {
				window := *d
				accepted := false
				if !busy {
					accepted = launch(window)
				} else if pending.pcm == nil {
					pending = window
					accepted = true
				}
				if accepted {
					d.pcm = make([]byte, 0, draftBytes)
				} else {
					d.pcm = d.pcm[:0]
					if !behind {
						behind = true
						publish()
					}
				}
			}
		case r := <-result:
			busy = false
			if !r.ok {
				behind = true
			}
			if pending.pcm != nil {
				if !launch(pending) {
					behind = true
				}
				pending = draftWindow{}
			}
			if r.text != "" {
				stable = strings.TrimSpace(stable + "\n" + r.track + ": " + r.text)
				chars := []rune(stable)
				if len(chars) > draftMaxRunes {
					stable = string(chars[len(chars)-draftMaxRunes:])
				}
			}
			// Stay visibly behind after any dropped window: a later result can't
			// fill the earlier gap. The authoritative batch run will rebuild it.
			publish()
		}
	}
}

func validDraftPCM(p sessiontypes.PCM) bool {
	return p.Track != "" && p.Rate >= 8000 && p.Rate <= 192000 && p.Channels >= 1 && p.Channels <= 8 && p.StartMS >= 0 && len(p.S16LE) > 0 && len(p.S16LE) <= p.Rate*p.Channels*2
}

func (d *draftWindow) append(p sessiontypes.PCM) (dropped bool) {
	first := audioFrame(p.StartMS, recognitionRate)
	if len(d.pcm) > 0 && (first > d.frame+recognitionRate/100 || first < d.frame-recognitionRate/100) {
		// A discontinuity cannot be presented as contiguous speech in the draft.
		d.pcm = d.pcm[:0]
		dropped = true
	}
	n := len(p.S16LE) / (p.Channels * 2)
	frames := n * recognitionRate / p.Rate
	for i := 0; i < frames; i++ {
		if len(d.pcm) == draftBytes {
			dropped = true
			break
		}
		index := i * p.Rate / recognitionRate * p.Channels * 2
		sample := 0
		for c := range p.Channels {
			sample += int(int16(binary.LittleEndian.Uint16(p.S16LE[index+c*2:])))
		}
		d.pcm = binary.LittleEndian.AppendUint16(d.pcm, uint16(int16(sample/p.Channels)))
	}
	d.frame = first + int64(frames)
	return dropped
}

func draftASR(ctx context.Context, asr Recognizer, opts Options, pcm []byte) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	f, err := os.CreateTemp("", "audiomemo-meeting-draft-*.wav")
	if err != nil {
		return "", false
	}
	defer os.Remove(f.Name())
	_, err = f.Write(wavHeader(int64(len(pcm)/2), recognitionRate, 1))
	if err == nil {
		_, err = f.Write(pcm)
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return "", false
	}
	r, err := asr(ctx, f.Name(), transcribe.TranscribeOpts{Language: opts.Language, Model: opts.nemoConfig().LiveModel})
	if err != nil || r == nil {
		return "", false
	}
	return strings.TrimSpace(r.Text), true
}
