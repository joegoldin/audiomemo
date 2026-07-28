package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
	"github.com/joegoldin/audiomemo/internal/transcribe"
	"github.com/joegoldin/audiomemo/internal/tui"
	"github.com/spf13/cobra"
)

var (
	rDuration       string
	rFormat         string
	rDevice         string
	rListDevices    bool
	rSampleRate     int
	rChannels       int
	rName           string
	rTemp           bool
	rTranscribe     bool
	rTranscribeArgs string
	rNoTUI          bool
	rVerbose        bool
	rConfig         string
	rClips          bool
)

var recordCmd = &cobra.Command{
	Use:     "record [flags] [name ...]",
	Aliases: []string{"rec"},
	Short:   "Record audio from microphone",
	Long: `Record audio from your microphone with a live transcript view. The cursor
at the end of the transcript doubles as a VU meter, changing height and color
with the mic level.

Live transcription streams automatically whenever an ElevenLabs API key is
configured. Press q to stop and keep the live transcript at <name>.txt; press
Q to stop and additionally run the higher-quality batch transcription, which
overwrites <name>.txt (the live preview is kept at <name>-live.txt either way).

An optional name can be passed as positional arguments to label the recording.
Multiple words are joined with underscores.

Examples:
  record
  record meeting
  rec standup -t
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
}

func ExecuteRecord() {
	rootCmd.SetArgs(append([]string{"record"}, os.Args[1:]...))
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
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

	if rClips {
		return runClips(cfg, name, format, sampleRate, channels, devices, deviceLabel, outputDir)
	}

	outputPath := filepath.Join(outputDir, record.GenerateFilename(format, name))

	var streamer *transcribe.Streamer
	streamNote := ""
	if cfg.Transcribe.ElevenLabs.APIKey != "" {
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

	if streamer != nil {
		transcriptPath := liveTranscriptPathFor(outputPath)
		if err := streamer.Start(context.Background(), rec.PCMReader, transcriptPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: live transcription failed to start: %v\n", err)
			streamNote = fmt.Sprintf("live transcription unavailable: %v", err)
			streamer = nil
			// ffmpeg was started with the PCM pipe output and nothing else will
			// read it. Drain it in the background so ffmpeg doesn't block on
			// pipe writes (which would freeze the primary encoded output too).
			go io.Copy(io.Discard, rec.PCMReader)
		}
	}

	shouldTranscribe := rTranscribe
	var model *tui.Model
	if rNoTUI {
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
	fmt.Println(outputPath)

	if shouldTranscribe {
		// Batch transcribe overwrites the promoted live transcript at
		// <base>.txt with the diarized full result. The live preview is
		// preserved at <base>-live.txt.
		return runPostTranscribe(outputPath)
	}

	return nil
}

func runClips(cfg *config.Config, name, format string, sampleRate, channels int, devices []string, deviceLabel, outputDir string) error {
	var savedPaths []string
	clipNumber := 1
	savedMessage := ""
	apiKey := cfg.Transcribe.ElevenLabs.APIKey

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
				return rec, nil, "live transcription unavailable: no ElevenLabs API key configured", nil
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

func runPostTranscribe(audioPath string) error {
	self, err := os.Executable()
	if err != nil {
		self = "transcribe"
	}

	args := []string{}
	if rVerbose {
		args = append(args, "--verbose")
	}
	if rTranscribeArgs != "" {
		args = append(args, strings.Fields(rTranscribeArgs)...)
	}
	args = append(args, audioPath)

	transcribeCmd := exec.Command(self, append([]string{"transcribe"}, args...)...)
	transcribeCmd.Stdout = os.Stdout
	transcribeCmd.Stderr = os.Stderr
	return transcribeCmd.Run()
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
