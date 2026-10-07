package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/transcribe"
	"github.com/spf13/cobra"
)

var (
	tBackend      string
	tModel        string
	tLanguage     string
	tOutput       string
	tFormat       string
	tVerbose      bool
	tCopy         bool
	tConfig       string
	tDiarize      bool
	tSmartFormat  bool
	tPunctuate    bool
	tFillerWords  bool
	tNumerals     bool
	tQuiet        bool
	tStoreInCloud bool
)

var transcribeCmd = &cobra.Command{
	Use:   "transcribe [flags] <file>",
	Short: "Transcribe audio to text",
	Long: `Transcribe locally with NeMo-Speech.cpp (Parakeet + Nemotron diarization).

Default auto mode tries local first, then ElevenLabs if a key is configured.
Use --backend nemo for local-only or --backend elevenlabs for cloud-only.

Examples:
  transcribe recording.ogg
  transcribe -b elevenlabs -f srt interview.wav
  transcribe -b deepgram -f srt interview.wav
  transcribe -b whisper -l en lecture.mp3
  cat audio.ogg | transcribe -`,
	Args: cobra.ExactArgs(1),
	RunE: runTranscribe,
}

func init() {
	transcribeCmd.AddCommand(transcribeLatestCmd, transcribeVoiceMemoCmd)
	transcribeCmd.PersistentFlags().StringVarP(&tBackend, "backend", "b", "", "transcription backend (auto, nemo, elevenlabs, whisper, whisper-cpp, whisperx, ffmpeg-whisper, deepgram, openai, mistral)")
	transcribeCmd.PersistentFlags().StringVarP(&tModel, "model", "m", "", "model name (backend-specific)")
	transcribeCmd.PersistentFlags().StringVarP(&tLanguage, "language", "l", "", "language hint (ISO 639-1)")
	transcribeCmd.PersistentFlags().StringVarP(&tOutput, "output", "o", "", "output file (replaces automatic sidecar)")
	transcribeCmd.PersistentFlags().StringVarP(&tFormat, "format", "f", "text", "output format (text, json, srt, vtt)")
	transcribeCmd.PersistentFlags().BoolVarP(&tVerbose, "verbose", "v", false, "show progress and timing info")
	transcribeCmd.PersistentFlags().BoolVarP(&tCopy, "copy", "C", false, "copy output to clipboard")
	transcribeCmd.PersistentFlags().StringVar(&tConfig, "config", "", "config file path")
	transcribeCmd.PersistentFlags().BoolVar(&tDiarize, "diarize", false, "enable speaker diarization")
	transcribeCmd.PersistentFlags().BoolVar(&tSmartFormat, "smart-format", false, "apply smart formatting (Deepgram)")
	transcribeCmd.PersistentFlags().BoolVar(&tPunctuate, "punctuate", false, "add punctuation (Deepgram)")
	transcribeCmd.PersistentFlags().BoolVar(&tFillerWords, "filler-words", false, "include filler words (Deepgram)")
	transcribeCmd.PersistentFlags().BoolVar(&tNumerals, "numerals", false, "convert numbers to numerals (Deepgram)")
	transcribeCmd.PersistentFlags().BoolVarP(&tQuiet, "quiet", "q", false, "save transcript to file without printing to stdout")
	transcribeCmd.PersistentFlags().BoolVar(&tStoreInCloud, "store-in-cloud", false, "keep transcript stored in cloud provider (ElevenLabs)")
}

func ExecuteTranscribe() {
	rootCmd.SetArgs(append([]string{"transcribe"}, os.Args[1:]...))
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runTranscribe(cmd *cobra.Command, args []string) error {
	return runTranscribeFile(cmd, args[0], transcriptPathFor(args[0], transcribe.ParseFormat(tFormat)))
}

func runTranscribeFile(cmd *cobra.Command, audioPath, savePath string) error {
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
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

	// Handle stdin
	if audioPath == "-" {
		tmp, err := bufferStdin()
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		audioPath = tmp
		savePath = transcriptPathFor(tmp, transcribe.ParseFormat(tFormat))
	}

	// Apply --store-in-cloud override before creating backend.
	if cmd.Flags().Changed("store-in-cloud") {
		cfg.Transcribe.ElevenLabs.StoreInCloud = tStoreInCloud
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
		case "auto", "nemo":
			diarize = cfg.Transcribe.Nemo.Diarize
		case "elevenlabs":
			diarize = cfg.Transcribe.ElevenLabs.Diarize
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

	language := tLanguage
	if language == "" {
		language = cfg.Transcribe.Language
	}
	opts := transcribe.TranscribeOpts{
		Model:       tModel,
		Language:    language,
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

	// An explicit destination replaces auto-save; comparison runs must not
	// overwrite an existing transcript beside the original recording.
	if savePath != "" && tOutput == "" {
		transcriptPath := savePath
		if err := os.WriteFile(transcriptPath, []byte(output), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save transcript to %s: %v\n", transcriptPath, err)
		} else if tVerbose {
			fmt.Fprintf(os.Stderr, "Saved transcript to %s\n", transcriptPath)
		}
	}

	if tOutput != "" {
		if err := os.WriteFile(tOutput, []byte(output), 0644); err != nil {
			return err
		}
	} else if !tQuiet {
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

// transcriptPathFor returns the path for a transcript file alongside the audio
// file, using the appropriate extension for the output format.
func transcriptPathFor(audioPath string, format transcribe.OutputFormat) string {
	ext := ".txt"
	switch format {
	case transcribe.FormatJSON:
		ext = ".json"
	case transcribe.FormatSRT:
		ext = ".srt"
	case transcribe.FormatVTT:
		ext = ".vtt"
	}
	base := strings.TrimSuffix(audioPath, filepath.Ext(audioPath))
	return base + ext
}

// liveTranscriptPathFor returns the path for the live realtime transcript
// alongside the audio file. The -live suffix keeps it separate from the batch
// transcript at <base>.txt so both are preserved after a -t recording.
func liveTranscriptPathFor(audioPath string) string {
	base := strings.TrimSuffix(audioPath, filepath.Ext(audioPath))
	return base + "-live.txt"
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
