package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegilkes/audiotools/internal/config"
	"github.com/joegilkes/audiotools/internal/record"
	"github.com/joegilkes/audiotools/internal/tui"
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
	rConfig         string
)

var recordCmd = &cobra.Command{
	Use:     "record [flags] [filename]",
	Aliases: []string{"rec"},
	Short:   "Record audio from microphone",
	Long: `Record audio from your microphone with a live TUI showing VU meter and animation.

Examples:
  record
  record -n meeting
  record -t
  record -d 5m --no-tui
  record -D "Built-in Microphone" -t --transcribe-args="--backend deepgram"`,
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
	device := cfg.Record.Device
	if rDevice != "" {
		device = rDevice
	}
	if device == "" {
		device = "default"
	}

	// Determine output path
	var outputPath string
	if len(args) > 0 {
		outputPath = args[0]
	} else {
		var outputDir string
		if rTemp {
			outputDir = os.TempDir()
		} else {
			outputDir = cfg.ResolveOutputDir()
		}
		if err := record.EnsureOutputDir(outputDir); err != nil {
			return fmt.Errorf("failed to create output dir: %w", err)
		}
		outputPath = filepath.Join(outputDir, record.GenerateFilename(format, rName))
	}

	opts := record.RecordOpts{
		Device:     device,
		Format:     format,
		SampleRate: sampleRate,
		Channels:   channels,
		OutputPath: outputPath,
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

	fmt.Fprintf(os.Stderr, "Saved: %s\n", outputPath)
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

	args := []string{audioPath}
	if rTranscribeArgs != "" {
		args = append(strings.Fields(rTranscribeArgs), audioPath)
	}

	transcribeCmd := exec.Command(self, append([]string{"transcribe"}, args...)...)
	transcribeCmd.Stdout = os.Stdout
	transcribeCmd.Stderr = os.Stderr
	return transcribeCmd.Run()
}
