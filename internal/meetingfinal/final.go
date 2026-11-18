// Package meetingfinal renders listening audio and runs local, source-separated
// recognition on closed meeting bundles. It never writes to original tracks.
package meetingfinal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/meetingcapture"
	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/joegoldin/audiomemo/internal/transcribe"
)

const maxWindowMS = 10 * 60 * 1000
const splitGapMS = 30 * 1000

type Recognizer func(context.Context, string, transcribe.TranscribeOpts) (*transcribe.Result, error)

type Options struct {
	Nemo     config.NemoConfig
	Language string
	Format   string     // optionally srt or vtt, in addition to the default text/JSON
	ASR      Recognizer // defaults to local NeMo, never generic backend dispatch
	Session  string     // preview session identity
}

func (o Options) nemoConfig() config.NemoConfig {
	if o.Nemo.Binary == "" {
		return config.Default().Transcribe.Nemo
	}
	return o.Nemo
}
func (o Options) recognizer() Recognizer {
	if o.ASR != nil {
		return o.ASR
	}
	return transcribe.NewNemo(o.nemoConfig()).Transcribe
}

var localModel = make(chan struct{}, 1)

// Finalize recovers and retries a closed bundle. A mix, recognition windows,
// text/JSON and subtitles can succeed independently and are never regenerated
// over edited files. Only a fully published result supplies final text.
func Finalize(ctx context.Context, bundle string, opts Options) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var text string
	err := meetingcapture.WithFinalization(bundle, func(p *meetingcapture.Publication) error {
		m := p.Manifest()
		if err := p.SetState("finalizing", m.Mix, m.Transcript, ""); err != nil {
			return err
		}
		tracks := make([][]meetingcapture.Segment, len(m.Tracks))
		sourceCount := 0
		partialCapture := m.Jobs["capture"].State == "interrupted" || m.State == "interrupted"
		for i, track := range m.Tracks {
			tracks[i] = p.Segments(track.ID)
			if len(tracks[i]) > 0 {
				sourceCount++
			}
			partialCapture = partialCapture || track.Error != "" || len(tracks[i]) == 0
		}
		if sourceCount == 0 {
			err := errors.New("no recoverable meeting audio")
			return errors.Join(err, p.SetState("failed", "", "", err.Error()))
		}
		mixErr := p.Publish("mix.wav", func(f *os.File) error {
			return renderWAV(ctx, f, p.Open, tracks, 0, m.DurationMS, mixRate, 2, 0.8/float64(sourceCount))
		})
		mix := ""
		if mixErr == nil {
			mix = "mix.wav"
		}
		mixJobErr := recordJob(p, "mix", mixErr)

		var transcript Transcript
		// Publish checks/reuses a previously published JSON without invoking ASR.
		jsonErr := p.Publish("transcript.json", func(f *os.File) error {
			result, err := recognize(ctx, p, opts, m.Tracks)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(f)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		})
		if jsonErr == nil {
			f, err := p.Open("transcript.json")
			if err == nil {
				err = json.NewDecoder(f).Decode(&transcript)
				f.Close()
			}
			jsonErr = err
		}
		asrJobErr := recordJob(p, "transcript_json", jsonErr)
		var textErr, subtitleErr error
		transcriptPath := ""
		if jsonErr == nil {
			text = transcript.text()
			textErr = p.Publish("transcript.txt", func(f *os.File) error { return writeText(f, text) })
			if textErr == nil {
				transcriptPath = "transcript.txt"
			}
			if format := transcribe.ParseFormat(strings.ToLower(opts.Format)); format == transcribe.FormatSRT || format == transcribe.FormatVTT {
				subtitleErr = p.Publish("transcript."+string(format), func(f *os.File) error { return writeText(f, transcript.subtitle(format)) })
				subtitleErr = errors.Join(subtitleErr, recordJob(p, string(format), subtitleErr))
			}
			textErr = errors.Join(textErr, recordJob(p, "transcript_text", textErr))
		}
		finalErr := errors.Join(mixErr, jsonErr, textErr, subtitleErr, mixJobErr, asrJobErr)
		state := "ready"
		if finalErr != nil || partialCapture {
			if mix != "" || transcriptPath != "" {
				state = "partial"
			} else {
				state = "failed"
			}
		}
		if partialCapture {
			finalErr = errors.Join(finalErr, errors.New("capture was incomplete; available audio and transcript are retained"))
		}
		message := ""
		if finalErr != nil {
			message = finalErr.Error()
		}
		return errors.Join(finalErr, p.SetState(state, mix, transcriptPath, message))
	})
	if err != nil {
		return "", err
	}
	return text, nil
}

func recordJob(p *meetingcapture.Publication, name string, err error) error {
	if err != nil {
		return p.SetJob(name, "failed", err.Error())
	}
	return p.SetJob(name, "ready", "")
}

func writeText(f *os.File, value string) error {
	if value != "" && !strings.HasSuffix(value, "\n") {
		value += "\n"
	}
	_, err := io.WriteString(f, value)
	return err
}

type window struct{ start, end int64 }

func windows(spans []meetingcapture.Segment) []window {
	var result []window
	for _, s := range spans {
		if len(result) > 0 {
			last := &result[len(result)-1]
			if s.StartMS-last.end <= splitGapMS && s.EndMS-last.start <= maxWindowMS {
				last.end = s.EndMS
				continue
			}
		}
		result = append(result, window{s.StartMS, s.EndMS})
	}
	return result
}

func recognize(ctx context.Context, p *meetingcapture.Publication, opts Options, tracks []sessiontypes.Track) (*Transcript, error) {
	t := &Transcript{Version: sessiontypes.Version, Notice: attributionNotice, Words: []Word{}, Turns: []Span{}, Excerpts: []Excerpt{}}
	asr := opts.recognizer()
	var failures []error
	for _, track := range tracks {
		spans := p.Segments(track.ID)
		for i, w := range windows(spans) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			cache := fmt.Sprintf("asr-%s-%06d.json", track.ID, i)
			err := p.Publish(cache, func(f *os.File) error {
				audio, err := os.CreateTemp("", "audiomemo-meeting-*.wav")
				if err != nil {
					return err
				}
				defer os.Remove(audio.Name())
				if err := errors.Join(renderWAV(ctx, audio, p.Open, [][]meetingcapture.Segment{spans}, w.start, w.end, recognitionRate, 1, 1), audio.Close()); err != nil {
					return err
				}
				select {
				case localModel <- struct{}{}:
					defer func() { <-localModel }()
				case <-ctx.Done():
					return ctx.Err()
				}
				result, err := asr(ctx, audio.Name(), transcribe.TranscribeOpts{Language: opts.Language, Diarize: opts.nemoConfig().Diarize})
				if err != nil || result == nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					// NeMo process output can contain recognized words. Never propagate
					// that output to daemon diagnostics or manifest errors.
					return fmt.Errorf("local NeMo failed for track %s window %d (check installed runtime and models)", track.ID, i+1)
				}
				return json.NewEncoder(f).Encode(result)
			})
			if err != nil {
				failures = append(failures, err)
				break
			}
			f, err := p.Open(cache)
			if err != nil {
				failures = append(failures, err)
				break
			}
			var result transcribe.Result
			err = json.NewDecoder(f).Decode(&result)
			f.Close()
			if err != nil {
				failures = append(failures, fmt.Errorf("read recognition cache for track %s window %d: %w", track.ID, i+1, err))
				break
			}
			annotate(t, &result, track, i+1, w.start, w.end)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return nil, err
	}
	t.sort()
	return t, nil
}
