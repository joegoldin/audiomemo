package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
	"github.com/joegoldin/audiomemo/internal/transcribe"
	"github.com/joegoldin/audiomemo/internal/tui"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// pcmSampleRate is the rate of the recorder's live PCM pipe, which is also
// what scribe keeps in memory for the final pass.
const pcmSampleRate = 16000

var (
	scNoRecord bool
	scCloud    bool
)

var scribeCmd = &cobra.Command{
	Use:   "scribe [flags] [name ...]",
	Short: "Private live transcription: local models, no audio saved",
	Long: `Transcribe your microphone live with the local NeMo models, and never write
the audio to disk. Only the transcript is saved, at <name>.txt in the output
directory.

By default the audio is held in memory for the length of the run, so Q (or -t)
can replace the live transcript with a final pass by the local batch model
when you stop. With -r/--no-record no audio is kept at all, not even in memory,
and the live transcript is the only result.

Nothing leaves the machine unless --cloud is passed. --cloud lets ElevenLabs
take over when the local live preview or final pass fails, which uploads the
audio.

Examples:
  scribe
  scribe standup
  scribe -r private thoughts
  scribe -t -d 10m | pbcopy`,
	Args: cobra.ArbitraryArgs,
	RunE: runScribe,
}

func init() {
	scribeCmd.Flags().BoolVarP(&scNoRecord, "no-record", "r", false, "keep no audio, not even in memory; the live transcript is the only result")
	scribeCmd.Flags().BoolVar(&scCloud, "cloud", false, "allow ElevenLabs as a fallback when local transcription fails (uploads audio)")
	scribeCmd.Flags().BoolVarP(&rTranscribe, "transcribe", "t", false, "always run the final pass on exit (as if quitting with Q)")
	scribeCmd.Flags().StringVarP(&rDevice, "device", "D", "", "input device name or index")
	scribeCmd.Flags().StringVarP(&rName, "name", "n", "", "label for the transcript filename")
	scribeCmd.Flags().StringVarP(&rMaxDuration, "max-duration", "d", "", "stop after this long (e.g. 30s, 5m, 1h30m)")
	scribeCmd.Flags().StringVar(&rMaxSilence, "max-silence", "", "stop after this much silence (e.g. 5s)")
	scribeCmd.Flags().Float64Var(&rSilenceDB, "silence-threshold", record.DefaultSilenceThreshold, "dBFS at or below which audio counts as silence")
	scribeCmd.Flags().StringVar(&rPrint, "print", "auto", "what to write to stdout: auto, path, text, both, none")
	scribeCmd.Flags().BoolVar(&rNoTUI, "no-tui", false, "headless mode")
	scribeCmd.Flags().BoolVarP(&rVerbose, "verbose", "v", false, "verbose output")
	scribeCmd.Flags().StringVar(&rConfig, "config", "", "config file path")
}

func ExecuteScribe() {
	rootCmd.SetArgs(append([]string{"scribe"}, os.Args[1:]...))
	execute()
}

// pcmTap keeps a copy of the live PCM in memory while forwarding it to the
// preview. It owns the read side of ffmpeg's pipe, so the copy is complete
// even when the preview stops reading early or never starts.
type pcmTap struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
}

// tapPCM starts copying src until EOF and returns the tap with the reader the
// preview should consume. Closing that reader stops the forwarding but not
// the copy.
func tapPCM(src io.Reader) (*pcmTap, io.ReadCloser) {
	t := &pcmTap{done: make(chan struct{})}
	preview, forward := io.Pipe()
	go func() {
		defer close(t.done)
		chunk := make([]byte, 32*1024)
		forwarding := true
		for {
			n, err := src.Read(chunk)
			if n > 0 {
				t.mu.Lock()
				t.buf.Write(chunk[:n])
				t.mu.Unlock()
				if forwarding {
					_, werr := forward.Write(chunk[:n])
					forwarding = werr == nil
				}
			}
			if err != nil {
				forward.Close()
				return
			}
		}
	}()
	return t, preview
}

// PCM waits for the recording's pipe to close and returns everything it
// carried.
func (t *pcmTap) PCM() []byte {
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.Bytes()
}

func runScribe(cmd *cobra.Command, args []string) error {
	var cfg *config.Config
	var err error
	if rConfig != "" {
		cfg, err = config.LoadFrom(rConfig)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if scNoRecord && rTranscribe {
		return fmt.Errorf("--transcribe needs the in-memory audio that --no-record turns off")
	}

	printFlag, err := parsePrintMode(rPrint)
	if err != nil {
		return err
	}

	stops := stopConditions{Threshold: rSilenceDB}
	if stops.MaxDuration, err = parseFlagDuration("--max-duration", rMaxDuration); err != nil {
		return err
	}
	if stops.MaxSilence, err = parseMaxSilence(rMaxSilence); err != nil {
		return err
	}

	var ui tuiTarget
	if !rNoTUI {
		ui = resolveTUITarget()
		defer ui.Close()
		warnTUITarget(ui)
		if !ui.Available {
			rNoTUI = true
		}
	}

	stdoutMode := resolvePrintMode(printFlag, isatty.IsTerminal(os.Stdout.Fd()), false)

	if err := maybeOnboard(cfg, rConfig, ui); err != nil {
		return err
	}

	cfg.ApplyEnv()

	cloud := scCloud && cfg.Transcribe.ElevenLabs.APIKey != ""
	if scCloud && !cloud {
		fmt.Fprintln(os.Stderr, "note: --cloud has no effect: no ElevenLabs API key configured")
	}
	if _, err := exec.LookPath(cfg.Transcribe.Nemo.Binary); err != nil && !cloud {
		return fmt.Errorf("scribe runs on local NeMo models, but %q is unavailable: %w (install nemo-speech, or pass --cloud to use ElevenLabs)", cfg.Transcribe.Nemo.Binary, err)
	}

	// "nemo" never reaches the cloud; "auto" adds the ElevenLabs fallback,
	// which only --cloud authorises.
	liveBackend := "nemo"
	if cloud {
		liveBackend = "auto"
	}
	streamer, err := transcribe.NewLiveStreamer(cfg, liveBackend)
	if err != nil {
		return err
	}

	devices, deviceLabel, skipped, err := resolveRecordDevices(cmd, cfg, ui)
	if err != nil {
		return err
	}
	if skipped {
		return nil
	}

	name := rName
	if len(args) > 0 {
		name = strings.Join(args, "_")
	}

	outputDir := cfg.ResolveOutputDir()
	if err := record.EnsureOutputDir(outputDir); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}
	// The transcript is the only file this command writes, so it takes the
	// name a recording would have had.
	transcriptPath := filepath.Join(outputDir, record.GenerateFilename("txt", name))

	// The encoded output goes to ffmpeg's null muxer. The audio that matters
	// arrives on the PCM pipe, which feeds the preview and, unless -r, the
	// in-memory copy.
	opts := stops.apply(record.RecordOpts{
		Device:      devices[0],
		Devices:     devices,
		DeviceLabel: deviceLabel,
		Format:      cfg.Record.Format,
		SampleRate:  cfg.Record.SampleRate,
		Channels:    cfg.Record.Channels,
		OutputPath:  os.DevNull,
		LivePCM:     true,
	})

	rec, err := record.Start(opts)
	if err != nil {
		return err
	}
	defer func() {
		rec.Stop()
		_ = rec.Wait()
		streamer.Stop()
		rec.PCMReader.Close()
	}()

	var tap *pcmTap
	previewPCM := io.Reader(rec.PCMReader)
	if !scNoRecord {
		tap, previewPCM = tapPCM(rec.PCMReader)
	}

	if err := streamer.Start(context.Background(), previewPCM, liveTranscriptPathFor(transcriptPath)); err != nil {
		return fmt.Errorf("live transcription failed to start: %w", err)
	}

	held := "audio held in memory"
	if tap == nil {
		held = "no audio kept"
	}

	shouldTranscribe := rTranscribe
	if rNoTUI {
		go func() {
			for warning := range streamer.Warning {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", warning)
			}
		}()
		go func() {
			for err := range streamer.Err {
				fmt.Fprintf(os.Stderr, "Live preview failed: %v\n", err)
			}
		}()
		fmt.Fprintf(os.Stderr, "Transcribing to %s, %s (%s)...\n", transcriptPath, held, stops.hint())
		if _, err := waitHeadless(rec); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: recording exited with error: %v\n", err)
		}
	} else {
		model := tui.NewModelWithStreamer(rec, opts, streamer)
		model.SetOutLabel(fmt.Sprintf("%s (%s)", transcriptPath, held))
		if tap == nil {
			model.DisableBatch()
		}
		p := tea.NewProgram(model, ui.Options(tea.WithAltScreen())...)
		if _, err := p.Run(); err != nil {
			return err
		}
		if err := rec.Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: recording exited with error: %v\n", err)
		}
		if model.ShouldTranscribe() {
			shouldTranscribe = true
		}
	}

	// Stopping the preview also reaps its model process, which has to be
	// gone before the final pass loads the larger batch model.
	streamer.Stop()

	if rec.StoppedForSilence() {
		fmt.Fprintf(os.Stderr, "Stopped after %s of silence.\n", stops.MaxSilence)
	}

	if _, err := promoteLiveTranscript(transcriptPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to promote live transcript: %v\n", err)
	}

	// With no live text to fall back on, asking for text on stdout is consent
	// to the final pass, as it is for record.
	if tap != nil && batchNeededForText(stdoutMode, transcriptHasText(transcriptPath), shouldTranscribe) {
		text, err := transcribeFromMemory(cfg, tap.PCM(), cloud)
		if err != nil {
			// The live transcript is already in place, so a failed upgrade
			// is not a failed run.
			fmt.Fprintf(os.Stderr, "Warning: final pass failed, keeping the live transcript: %v\n", err)
		} else if text != "" {
			if err := os.WriteFile(transcriptPath, []byte(text+"\n"), 0600); err != nil {
				return fmt.Errorf("failed to save transcript: %w", err)
			}
		}
	}

	text := resolveStdoutText("", transcriptPath, os.ReadFile)
	if text == "" {
		fmt.Fprintln(os.Stderr, "Warning: no transcript was produced")
		return nil
	}
	if stdoutMode == printPath || stdoutMode == printBoth {
		fmt.Println(transcriptPath)
	}
	if wantsStdoutText(stdoutMode) {
		fmt.Println(text)
	}
	return nil
}

// transcriptHasText reports whether the live preview produced anything.
func transcriptHasText(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(data)) != ""
}

// transcribeFromMemory runs the final pass over the in-memory PCM: the local
// batch model first, and ElevenLabs after it only when cloud is allowed.
func transcribeFromMemory(cfg *config.Config, pcm []byte, cloud bool) (string, error) {
	if len(pcm) == 0 {
		return "", errors.New("no audio was captured")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	opts := transcribe.TranscribeOpts{Language: cfg.Transcribe.Language, Verbose: rVerbose}
	if rVerbose {
		fmt.Fprintf(os.Stderr, "Final pass over %d bytes of audio in memory with nemo...\n", len(pcm))
	}
	res, err := transcribe.NewNemo(cfg.Transcribe.Nemo).TranscribePCM(ctx, pcm, pcmSampleRate, opts)
	if err != nil && cloud && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "Local final pass failed: %v\nFalling back to ElevenLabs (--cloud); the audio will be uploaded.\n", err)
		el := transcribe.NewElevenLabs(cfg.Transcribe.ElevenLabs.APIKey, cfg.Transcribe.ElevenLabs.Model, cfg.Transcribe.ElevenLabs.StoreInCloud)
		audio := io.MultiReader(bytes.NewReader(transcribe.WAVHeader(len(pcm), pcmSampleRate, 1)), bytes.NewReader(pcm))
		res, err = el.TranscribeReader(ctx, audio, "scribe.wav", opts)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Format(transcribe.FormatText)), nil
}
