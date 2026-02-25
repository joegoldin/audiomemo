package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
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
)

var recordCmd = &cobra.Command{
	Use:     "record [flags] [name]",
	Aliases: []string{"rec"},
	Short:   "Record audio from microphone",
	Long: `Record audio from your microphone with a live TUI showing VU meter and animation.

An optional name can be passed as a positional argument to label the recording.

Examples:
  record
  record meeting
  rec standup -t
  record -d 5m --no-tui
  record -D "Built-in Microphone" -t --transcribe-args="--backend deepgram"`,
	Args: cobra.MaximumNArgs(1),
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
	recordCmd.Flags().BoolVarP(&rTranscribe, "transcribe", "t", false, "transcribe after recording")
	recordCmd.Flags().StringVar(&rTranscribeArgs, "transcribe-args", "", "extra args for transcribe")
	recordCmd.Flags().BoolVar(&rNoTUI, "no-tui", false, "headless mode")
	recordCmd.Flags().BoolVarP(&rVerbose, "verbose", "v", false, "verbose output (passed to transcribe)")
	recordCmd.Flags().StringVar(&rConfig, "config", "", "config file path")
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

		// Build a human-readable label for the TUI mic line.
		deviceLabel = deviceName
		if group, ok := cfg.DeviceGroups[deviceName]; ok && len(group) > 1 {
			deviceLabel = fmt.Sprintf("%s (%s)", deviceName, strings.Join(group, " + "))
		}
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
	// Positional arg takes priority, then -n flag.
	name := rName
	if len(args) > 0 && args[0] != "" {
		name = args[0]
	}
	outputPath := filepath.Join(outputDir, record.GenerateFilename(format, name))

	opts := record.RecordOpts{
		Device:      devices[0],
		Devices:     devices,
		DeviceLabel: deviceLabel,
		Format:      format,
		SampleRate:  sampleRate,
		Channels:    channels,
		OutputPath:  outputPath,
	}

	rec, err := record.Start(opts)
	if err != nil {
		return err
	}

	shouldTranscribe := rTranscribe
	if rNoTUI {
		fmt.Fprintf(os.Stderr, "Recording to %s (Ctrl+C to stop)...\n", outputPath)
		if err := <-rec.Done; err != nil {
			return err
		}
	} else {
		model := tui.NewModel(rec, opts)
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return err
		}
		// Wait for ffmpeg to fully exit and finalize the output file
		if err := rec.Wait(); err != nil {
			return fmt.Errorf("recording failed: %w", err)
		}
		if model.ShouldTranscribe() {
			shouldTranscribe = true
		}
	}

	// Print just the path to stdout so it can be piped, e.g.:
	//   transcribe $(record)
	fmt.Println(outputPath)

	if shouldTranscribe {
		return runPostTranscribe(outputPath)
	}

	return nil
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
