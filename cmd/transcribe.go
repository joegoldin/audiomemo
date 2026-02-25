package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/transcribe"
	"github.com/spf13/cobra"
)

var (
	tBackend     string
	tModel       string
	tLanguage    string
	tOutput      string
	tFormat      string
	tVerbose     bool
	tCopy        bool
	tConfig      string
	tDiarize     bool
	tSmartFormat bool
	tPunctuate   bool
	tFillerWords bool
	tNumerals    bool
)

var transcribeCmd = &cobra.Command{
	Use:   "transcribe [flags] <file>",
	Short: "Transcribe audio to text",
	Long: `Transcribe audio files using local whisper or cloud APIs (Deepgram, OpenAI, Mistral).

By default, auto-detects the best available backend. Use --backend to force a specific one.

Examples:
  transcribe recording.ogg
  transcribe -b deepgram -f srt interview.wav
  transcribe -b whisper -l en lecture.mp3
  cat audio.ogg | transcribe -`,
	Args: cobra.ExactArgs(1),
	RunE: runTranscribe,
}

func init() {
	transcribeCmd.AddCommand(transcribeLatestCmd)
	transcribeCmd.PersistentFlags().StringVarP(&tBackend, "backend", "b", "", "transcription backend (whisper, whisper-cpp, whisperx, ffmpeg-whisper, deepgram, openai, mistral)")
	transcribeCmd.PersistentFlags().StringVarP(&tModel, "model", "m", "", "model name (backend-specific)")
	transcribeCmd.PersistentFlags().StringVarP(&tLanguage, "language", "l", "", "language hint (ISO 639-1)")
	transcribeCmd.PersistentFlags().StringVarP(&tOutput, "output", "o", "", "output file (default: stdout)")
	transcribeCmd.PersistentFlags().StringVarP(&tFormat, "format", "f", "text", "output format (text, json, srt, vtt)")
	transcribeCmd.PersistentFlags().BoolVarP(&tVerbose, "verbose", "v", false, "show progress and timing info")
	transcribeCmd.PersistentFlags().BoolVarP(&tCopy, "copy", "C", false, "copy output to clipboard")
	transcribeCmd.PersistentFlags().StringVar(&tConfig, "config", "", "config file path")
	transcribeCmd.PersistentFlags().BoolVar(&tDiarize, "diarize", false, "enable speaker diarization")
	transcribeCmd.PersistentFlags().BoolVar(&tSmartFormat, "smart-format", false, "apply smart formatting (Deepgram)")
	transcribeCmd.PersistentFlags().BoolVar(&tPunctuate, "punctuate", false, "add punctuation (Deepgram)")
	transcribeCmd.PersistentFlags().BoolVar(&tFillerWords, "filler-words", false, "include filler words (Deepgram)")
	transcribeCmd.PersistentFlags().BoolVar(&tNumerals, "numerals", false, "convert numbers to numerals (Deepgram)")
}

func ExecuteTranscribe() {
	rootCmd.SetArgs(append([]string{"transcribe"}, os.Args[1:]...))
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runTranscribe(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var cfg *config.Config
	var err error
	if tConfig != "" {
		cfg, err = config.LoadFrom(tConfig)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	cfg.ApplyEnv()

	audioPath := args[0]

	// Handle stdin
	if audioPath == "-" {
		tmp, err := bufferStdin()
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		audioPath = tmp
	}

	backend, err := transcribe.NewDispatcher(cfg, tBackend)
	if err != nil {
		return err
	}

	// Merge config defaults with CLI flags for diarize/smart-format/punctuate.
	// CLI flags override config defaults. Determine active backend name to read
	// the correct config section.
	diarize := tDiarize
	smartFormat := tSmartFormat
	punctuate := tPunctuate

	if !cmd.Flags().Changed("diarize") {
		switch backend.Name() {
		case "deepgram":
			diarize = cfg.Transcribe.Deepgram.Diarize
		case "whisperx":
			diarize = cfg.Transcribe.Whisper.Diarize
		}
	}
	if !cmd.Flags().Changed("smart-format") {
		if backend.Name() == "deepgram" {
			smartFormat = cfg.Transcribe.Deepgram.SmartFormat
		}
	}
	if !cmd.Flags().Changed("punctuate") {
		if backend.Name() == "deepgram" {
			punctuate = cfg.Transcribe.Deepgram.Punctuate
		}
	}

	fillerWords := tFillerWords
	numerals := tNumerals

	if !cmd.Flags().Changed("filler-words") {
		if backend.Name() == "deepgram" {
			fillerWords = cfg.Transcribe.Deepgram.FillerWords
		}
	}
	if !cmd.Flags().Changed("numerals") {
		if backend.Name() == "deepgram" {
			numerals = cfg.Transcribe.Deepgram.Numerals
		}
	}

	opts := transcribe.TranscribeOpts{
		Model:       tModel,
		Language:    tLanguage,
		Format:      transcribe.ParseFormat(tFormat),
		Verbose:     tVerbose,
		Diarize:     diarize,
		SmartFormat: smartFormat,
		Punctuate:   punctuate,
		FillerWords: fillerWords,
		Numerals:    numerals,
	}

	if tVerbose {
		fmt.Fprintf(os.Stderr, "Transcribing with %s...\n", backend.Name())
	}

	start := time.Now()

	// Show elapsed time ticker when verbose
	done := make(chan struct{})
	if tVerbose {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case t := <-ticker.C:
					elapsed := t.Sub(start).Truncate(time.Second)
					fmt.Fprintf(os.Stderr, "  %s elapsed...\n", elapsed)
				}
			}
		}()
	}

	result, err := backend.Transcribe(ctx, audioPath, opts)
	close(done)
	if err != nil {
		return err
	}

	if tVerbose {
		elapsed := time.Since(start).Truncate(time.Millisecond)
		fmt.Fprintf(os.Stderr, "Done in %s\n", elapsed)
	}

	output := result.Format(opts.Format)

	if tOutput != "" {
		if err := os.WriteFile(tOutput, []byte(output), 0644); err != nil {
			return err
		}
	} else {
		fmt.Println(output)
	}

	if tCopy {
		if err := copyToClipboard(output); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to copy to clipboard: %v\n", err)
		} else if tVerbose {
			fmt.Fprintln(os.Stderr, "Copied to clipboard")
		}
	}

	return nil
}

func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		// Try wl-copy (Wayland) first, fall back to xclip (X11)
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func bufferStdin() (string, error) {
	tmp, err := os.CreateTemp("", "audiomemo-stdin-*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, os.Stdin); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}
