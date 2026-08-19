package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
	"github.com/joegoldin/audiomemo/internal/stream"
	"github.com/joegoldin/audiomemo/internal/transcribe"
	"github.com/joegoldin/audiomemo/internal/tui"
	"github.com/spf13/cobra"
)

var (
	rDuration        string
	rFormat          string
	rDevice          string
	rListDevices     bool
	rSampleRate      int
	rChannels        int
	rName            string
	rTemp            bool
	rTranscribe      bool
	rTranscribeArgs  string
	rNoTUI           bool
	rVerbose         bool
	rConfig          string
	rClips           bool
	rNoLive          bool
	rWhisperShortcut bool
	rStream          bool
)

var recordCmd = &cobra.Command{
	Use:     "record [flags] [name ...]",
	Aliases: []string{"rect"},
	Short:   "Record audio from microphone",
	Long: `Record audio from your microphone with a live transcript view. The cursor
at the end of the transcript doubles as a VU meter, changing height and color
with the mic level.

Live transcription streams automatically whenever an ElevenLabs API key is
configured unless --no-live-transcription is passed. Press q to stop and keep
the live transcript at <name>.txt; press Q to stop and additionally run the
higher-quality batch transcription, which overwrites <name>.txt (the live
preview is kept at <name>-live.txt either way).

An optional name can be passed as positional arguments to label the recording.
Multiple words are joined with underscores.

Examples:
  record
  record meeting
  rect standup -t
  recw private notes
  record -d 5m --no-tui
  record -D "Built-in Microphone" -t --transcribe-args="--backend deepgram"`,
	Args: cobra.ArbitraryArgs,
	RunE: runRecord,
}

func init() {
	recordCmd.Flags().StringVarP(&rDuration, "duration", "d", "", "max recording duration (e.g. 5m, 1h30m)")
	recordCmd.Flags().StringVar(&rFormat, "format", "", "output format (ogg, wav, flac, mp3)")
	recordCmd.Flags().StringVarP(&rDevice, "device", "D", "", "input device name or index")
	recordCmd.Flags().BoolVarP(&rListDevices, "list-devices", "L", false, "list available input devices")
	recordCmd.Flags().IntVarP(&rSampleRate, "sample-rate", "r", 0, "sample rate in Hz")
	recordCmd.Flags().IntVarP(&rChannels, "channels", "c", 0, "channel count (1=mono, 2=stereo)")
	recordCmd.Flags().StringVarP(&rName, "name", "n", "", "label for filename")
	recordCmd.Flags().BoolVar(&rTemp, "temp", false, "save to temp directory")
	recordCmd.Flags().BoolVarP(&rTranscribe, "transcribe", "t", false, "always run batch transcription on exit (as if quitting with Q)")
	recordCmd.Flags().StringVar(&rTranscribeArgs, "transcribe-args", "", "extra args for transcribe")
	recordCmd.Flags().BoolVar(&rNoTUI, "no-tui", false, "headless mode")
	recordCmd.Flags().BoolVarP(&rVerbose, "verbose", "v", false, "verbose output (passed to transcribe)")
	recordCmd.Flags().StringVar(&rConfig, "config", "", "config file path")
	recordCmd.Flags().BoolVarP(&rClips, "clips", "C", false, "clips mode: record multiple clips sequentially")
	recordCmd.Flags().BoolVar(&rNoLive, "no-live-transcription", false, "disable live transcription while recording")
	recordCmd.Flags().BoolVar(&rStream, "stream", false, "emit newline-delimited JSON events on stdout while recording (implies --no-tui)")
}

func ExecuteRecord() {
	rootCmd.SetArgs(append([]string{"record"}, os.Args[1:]...))
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// ExecuteRecordWhisper runs the recw shortcut: record without live
// transcription, then batch-transcribe using only a local Whisper backend.
func ExecuteRecordWhisper() {
	rWhisperShortcut = true
	ExecuteRecord()
}

// reportLiveUnavailable decides whether a stream consumer should be told, as an
// error event, that live transcription is not running.
//
// Only when it was wanted and could not be had. --no-live-transcription is a
// choice rather than a failure, and start.mode already reports it as "none";
// emitting an error there would raise a failure banner over a flag the user
// deliberately passed.
func reportLiveUnavailable(streamNote string, streaming, liveDisabled bool) bool {
	return streamNote != "" && streaming && !liveDisabled
}

func resolveRecordTranscriptionMode(noLiveFlag, whisperShortcut, transcribeFlag bool) (liveDisabled, batchTranscribe bool) {
	return noLiveFlag || whisperShortcut, transcribeFlag || whisperShortcut
}

// validateStreamFlags rejects the combinations --stream cannot honour. Both
// rejected modes want stdout for something other than NDJSON, and a consumer
// parsing one object per line would choke on either.
func validateStreamFlags(streamFlag, clips, listDevices bool) error {
	if !streamFlag {
		return nil
	}
	if clips {
		return fmt.Errorf("--stream cannot be combined with --clips: clips mode is an interactive TUI loop")
	}
	if listDevices {
		return fmt.Errorf("--stream cannot be combined with --list-devices: use `audiomemo device list`")
	}
	return nil
}

// resolveStreamMode reports what the start event should claim. It answers one
// question for the consumer: will partial events arrive? A batch pass alone
// produces a single final at the end and nothing before it.
func resolveStreamMode(liveActive, batchTranscribe bool) string {
	switch {
	case liveActive:
		return stream.ModeLive
	case batchTranscribe:
		return stream.ModeBatch
	default:
		return stream.ModeNone
	}
}

func runRecord(cmd *cobra.Command, args []string) error {
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

	if err := validateStreamFlags(rStream, rClips, rListDevices); err != nil {
		return err
	}
	// A bubbletea alternate screen and an NDJSON consumer cannot both own
	// stdout. Forcing headless mode here also suppresses the interactive
	// device picker below, which a machine consumer could not answer.
	if rStream {
		rNoTUI = true
	}

	if err := maybeOnboard(cfg, rConfig); err != nil {
		return err
	}

	cfg.ApplyEnv()

	if rListDevices {
		devices, err := record.ListDevices()
		if err != nil {
			return fmt.Errorf("failed to list devices: %w", err)
		}
		for _, d := range devices {
			def := ""
			if d.IsDefault {
				def = " (default)"
			}
			fmt.Printf("  %s [%s]%s\n", d.Name, d.Description, def)
		}
		return nil
	}

	// Merge config with flags
	format := cfg.Record.Format
	if rFormat != "" {
		format = rFormat
	}
	sampleRate := cfg.Record.SampleRate
	if rSampleRate != 0 {
		sampleRate = rSampleRate
	}
	channels := cfg.Record.Channels
	if rChannels != 0 {
		channels = rChannels
	}

	var devices []string
	var deviceLabel string

	if !cmd.Flags().Changed("device") && !rNoTUI {
		result, err := tui.RunRecordPicker(cfg)
		if err != nil {
			return err
		}
		if result.Skipped {
			return nil
		}
		devices = result.Devices
		deviceLabel = result.DeviceLabel
	} else {
		deviceName := cfg.Record.Device
		if rDevice != "" {
			deviceName = rDevice
		}
		if deviceName == "" {
			deviceName = "default"
		}

		devices, err = cfg.ResolveDevice(deviceName)
		if err != nil {
			return fmt.Errorf("failed to resolve device %q: %w", deviceName, err)
		}

		deviceLabel = deviceName
		if group, ok := cfg.DeviceGroups[deviceName]; ok && len(group) > 1 {
			deviceLabel = fmt.Sprintf("%s (%s)", deviceName, strings.Join(group, " + "))
		}
	}

	// Resolve pretty/description names to raw PulseAudio names, and
	// fuzzy-substitute names that no longer exist verbatim (e.g. a PulseAudio
	// profile rename like "HiFi__Line1__sink" -> "HiFi__Line__sink"). Applied
	// for both the TUI-picker and explicit-device paths so a stale alias
	// stored in config doesn't blow up ffmpeg.
	if availDevices, listErr := record.ListDevices(); listErr == nil {
		devices = record.ResolveDeviceNames(devices, availDevices)
		for i, n := range devices {
			if record.HasDevice(n, availDevices) {
				continue
			}
			if matched, ok := record.FuzzyMatchDevice(n, availDevices); ok {
				fmt.Fprintf(os.Stderr, "warning: device %q not found; auto-substituting %q (fuzzy match)\n", n, matched.Name)
				devices[i] = matched.Name
			}
		}
	}

	// Positional args take priority, then -n flag. Multiple words joined with _.
	name := rName
	if len(args) > 0 {
		name = strings.Join(args, "_")
	}

	if rClips && name == "" {
		return fmt.Errorf("clips mode requires a name: record --clips <name>")
	}

	// Determine output path
	var outputDir string
	if rTemp {
		outputDir = os.TempDir()
	} else {
		outputDir = cfg.ResolveOutputDir()
	}
	if err := record.EnsureOutputDir(outputDir); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	liveDisabled, shouldTranscribe := resolveRecordTranscriptionMode(rNoLive, rWhisperShortcut, rTranscribe)

	if rClips {
		return runClips(cfg, name, format, sampleRate, channels, devices, deviceLabel, outputDir, liveDisabled)
	}

	outputPath := filepath.Join(outputDir, record.GenerateFilename(format, name))

	var streamer *transcribe.Streamer
	streamNote := ""
	if liveDisabled {
		streamNote = "live transcription disabled"
	} else if cfg.Transcribe.ElevenLabs.APIKey != "" {
		streamer = transcribe.NewStreamer(
			cfg.Transcribe.ElevenLabs.APIKey,
			cfg.Transcribe.ElevenLabs.StoreInCloud,
		)
	} else {
		streamNote = "live transcription unavailable: no ElevenLabs API key configured"
	}

	opts := record.RecordOpts{
		Device:      devices[0],
		Devices:     devices,
		DeviceLabel: deviceLabel,
		Format:      format,
		SampleRate:  sampleRate,
		Channels:    channels,
		OutputPath:  outputPath,
		LivePCM:     streamer != nil,
	}

	rec, err := record.Start(opts)
	if err != nil {
		return err
	}

	var streamStartErr error
	if streamer != nil {
		transcriptPath := liveTranscriptPathFor(outputPath)
		if err := streamer.Start(context.Background(), rec.PCMReader, transcriptPath); err != nil {
			if !rStream {
				fmt.Fprintf(os.Stderr, "Warning: live transcription failed to start: %v\n", err)
			}
			streamStartErr = err
			streamNote = fmt.Sprintf("live transcription unavailable: %v", err)
			streamer = nil
			// ffmpeg was started with the PCM pipe output and nothing else will
			// read it. Drain it in the background so ffmpeg doesn't block on
			// pipe writes (which would freeze the primary encoded output too).
			go io.Copy(io.Discard, rec.PCMReader)
		}
	} else if reportLiveUnavailable(streamNote, rStream, liveDisabled) {
		streamStartErr = errors.New(streamNote)
	}

	var model *tui.Model
	if rStream {
		// The start event carries the same facts as the stderr line the plain
		// headless path prints, so that line is redundant here.
		return runRecordStream(cfg, opts, rec, streamer, streamStartErr, shouldTranscribe)
	} else if rNoTUI {
		fmt.Fprintf(os.Stderr, "Recording to %s (Ctrl+C to stop)...\n", outputPath)
		if err := <-rec.Done; err != nil {
			return err
		}
	} else {
		if streamer != nil {
			model = tui.NewModelWithStreamer(rec, opts, streamer)
		} else {
			model = tui.NewModel(rec, opts)
			model.SetStreamNote(streamNote)
		}
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return err
		}
		// Wait for ffmpeg to fully exit and finalize the output file. ffmpeg
		// can exit non-zero even after writing a valid file — notably when
		// the live-streaming PCM pipe tears down on 'q', it sometimes
		// surfaces broken-pipe warnings as a non-zero status. Treat any
		// error as a warning so the post-recording batch transcribe still
		// runs; if the audio file is actually corrupt the batch step will
		// fail loudly on its own.
		if err := rec.Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: recording exited with error: %v\n", err)
		}
		if model.ShouldTranscribe() {
			shouldTranscribe = true
		}
	}

	if streamer != nil {
		streamer.Stop()
	}

	// Promote the live transcript to the canonical <base>.txt so a transcript
	// always exists. When batch transcription runs next (Q or -t), it
	// overwrites the canonical file with the higher-quality result.
	if promoted, err := promoteLiveTranscript(outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to promote live transcript: %v\n", err)
	} else if promoted != "" && rVerbose {
		fmt.Fprintf(os.Stderr, "Saved live transcript to %s\n", promoted)
	}

	// Print just the path to stdout so it can be piped, e.g.:
	//   transcribe $(record)
	// Under --stream, stdout carries NDJSON and a bare path line would break
	// a line-oriented consumer; path travels on the start, final, and end
	// events instead.
	if !rStream {
		fmt.Println(outputPath)
	}

	if shouldTranscribe {
		// Batch transcribe overwrites the promoted live transcript at
		// <base>.txt with the diarized full result. The live preview is
		// preserved at <base>-live.txt.
		return runPostTranscribe(outputPath)
	}

	return nil
}

func runClips(cfg *config.Config, name, format string, sampleRate, channels int, devices []string, deviceLabel, outputDir string, liveDisabled bool) error {
	var savedPaths []string
	clipNumber := 1
	savedMessage := ""
	apiKey := ""
	streamNote := "live transcription disabled"
	if !liveDisabled {
		apiKey = cfg.Transcribe.ElevenLabs.APIKey
		if apiKey == "" {
			streamNote = "live transcription unavailable: no ElevenLabs API key configured"
		}
	}

	for {
		outputPath := filepath.Join(outputDir, record.GenerateClipFilename(format, name, clipNumber))
		livePath := liveTranscriptPathFor(outputPath)
		opts := record.RecordOpts{
			Device:      devices[0],
			Devices:     devices,
			DeviceLabel: deviceLabel,
			Format:      format,
			SampleRate:  sampleRate,
			Channels:    channels,
			OutputPath:  outputPath,
			LivePCM:     apiKey != "",
		}

		// Streamers are single-use (Stop closes their channels), so each clip
		// gets a fresh one. clipStreamer holds the streamer created by
		// startRec so it can be stopped after the clip's TUI exits.
		var clipStreamer *transcribe.Streamer
		startRec := func() (*record.Recorder, *transcribe.Streamer, string, error) {
			rec, err := record.Start(opts)
			if err != nil {
				return nil, nil, "", err
			}
			if apiKey == "" {
				return rec, nil, streamNote, nil
			}
			s := transcribe.NewStreamer(apiKey, cfg.Transcribe.ElevenLabs.StoreInCloud)
			if err := s.Start(context.Background(), rec.PCMReader, livePath); err != nil {
				// Nothing else reads the PCM pipe; drain it so ffmpeg doesn't
				// block on pipe writes. This clip records without live text;
				// the next clip retries with a fresh streamer.
				go io.Copy(io.Discard, rec.PCMReader)
				return rec, nil, fmt.Sprintf("live transcription unavailable: %v", err), nil
			}
			clipStreamer = s
			return rec, s, "", nil
		}

		var model *tui.Model
		if clipNumber == 1 {
			// First clip: start recording immediately
			rec, streamer, note, err := startRec()
			if err != nil {
				return err
			}
			model = tui.NewClipsModel(nil, rec, streamer, opts, clipNumber, "")
			model.SetStreamNote(note)
		} else {
			// Subsequent clips: show ready state, wait for user to start
			model = tui.NewClipsModel(startRec, nil, nil, opts, clipNumber, savedMessage)
		}

		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return err
		}

		rec := model.Recorder()
		recorded := rec != nil

		if recorded {
			err := rec.Wait()
			if clipStreamer != nil {
				clipStreamer.Stop()
			}
			if err != nil {
				// ffmpeg can exit non-zero even after writing a valid file —
				// notably when the live PCM pipe tears down on 'q'. Treat as a
				// warning; if the file is corrupt the batch step fails loudly.
				fmt.Fprintf(os.Stderr, "Warning: recording exited with error: %v\n", err)
			}
			savedPaths = append(savedPaths, outputPath)
			fmt.Println(outputPath)
			if _, perr := promoteLiveTranscript(outputPath); perr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to promote live transcript: %v\n", perr)
			}
		}

		if model.ShouldTranscribe() {
			for _, path := range savedPaths {
				if err := runPostTranscribe(path); err != nil {
					fmt.Fprintf(os.Stderr, "transcribe %s: %v\n", path, err)
				}
			}
			return nil
		}

		if model.ClipDone() {
			savedMessage = fmt.Sprintf("Saved clip %d!", clipNumber)
			clipNumber++
			continue
		}

		// ctrl+c or q from ready state — done
		return nil
	}
}

// newPostTranscribeCmd builds the batch transcription subprocess and returns
// the argument list alongside it, so callers can inspect which backend the
// subprocess will resolve without re-deriving it.
func newPostTranscribeCmd(audioPath string) (*exec.Cmd, []string, error) {
	self, err := os.Executable()
	if err != nil {
		self = "transcribe"
	}
	args, err := buildPostTranscribeArgs(audioPath, rTranscribeArgs, rVerbose, rWhisperShortcut, rStream, exec.LookPath)
	if err != nil {
		return nil, nil, err
	}
	return exec.Command(self, append([]string{"transcribe"}, args...)...), args, nil
}

func runPostTranscribe(audioPath string) error {
	cmd, _, err := newPostTranscribeCmd(audioPath)
	if err != nil {
		return err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runPostTranscribeCapture runs the same batch pass with stdout captured.
// Under --stream, stdout carries NDJSON, and a transcript written straight
// into it would break the consumer's line parser. Stderr still goes to fd 2:
// whisper's and ffmpeg's diagnostics are not audiomemo's to reformat.
func runPostTranscribeCapture(audioPath string) (string, []string, error) {
	cmd, args, err := newPostTranscribeCmd(audioPath)
	if err != nil {
		return "", nil, err
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	return strings.TrimRight(out.String(), "\n"), args, err
}

// mentionsDiarize reports whether the caller already decided about speaker
// labels, in any of the spellings the flag package accepts.
func mentionsDiarize(transcribeArgs string) bool {
	for _, f := range strings.Fields(transcribeArgs) {
		if f == "--diarize" || strings.HasPrefix(f, "--diarize=") {
			return true
		}
	}
	return false
}

func buildPostTranscribeArgs(audioPath, transcribeArgs string, verbose, localWhisperOnly, streaming bool, lookPath func(string) (string, error)) ([]string, error) {
	args := []string{}
	if verbose {
		args = append(args, "--verbose")
	}
	// A stream consumer is feeding the text somewhere it will be used as
	// prose: an editor, a prompt, a note. Speaker labels are noise there, and
	// both cloud backends default Diarize to true in config, so the label
	// arrives without anyone asking for it.
	//
	// This goes ahead of the user's own args so `--transcribe-args "--diarize"`
	// still wins, which is the escape hatch for the rare streamed recording of
	// an actual conversation.
	if streaming && !mentionsDiarize(transcribeArgs) {
		args = append(args, "--diarize=false")
	}
	if transcribeArgs != "" {
		args = append(args, strings.Fields(transcribeArgs)...)
	}
	if localWhisperOnly {
		backend, err := preferredLocalWhisperBackend(lookPath)
		if err != nil {
			return nil, err
		}
		// Append this after user-supplied transcribe args so recw cannot be
		// redirected to a cloud backend.
		args = append(args, "--backend", backend)
	}
	return append(args, audioPath), nil
}

func preferredLocalWhisperBackend(lookPath func(string) (string, error)) (string, error) {
	if _, err := lookPath("whisper-cli"); err == nil {
		return "whisper-cpp", nil
	}
	if _, err := lookPath("whisper"); err == nil {
		return "whisper", nil
	}
	return "", fmt.Errorf("recw requires a local Whisper backend: install whisper-cpp (whisper-cli) or whisper")
}

// promoteLiveTranscript copies the live transcript (<base>-live.txt) to the
// canonical transcript path (<base>.txt) so a transcript always exists after
// recording, even without a batch run. The live file is preserved; a later
// batch transcription overwrites the canonical file with the diarized result.
// Missing or blank live files are skipped (returns "", nil).
func promoteLiveTranscript(audioPath string) (string, error) {
	livePath := liveTranscriptPathFor(audioPath)
	data, err := os.ReadFile(livePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", nil
	}
	dest := transcriptPathFor(audioPath, transcribe.FormatText)
	if err := os.WriteFile(dest, data, 0644); err != nil {
		return "", err
	}
	return dest, nil
}
