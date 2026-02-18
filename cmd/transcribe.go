package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/joegilkes/audiotools/internal/config"
	"github.com/joegilkes/audiotools/internal/transcribe"
	"github.com/spf13/cobra"
)

var (
	tBackend  string
	tModel    string
	tLanguage string
	tOutput   string
	tFormat   string
	tVerbose  bool
	tConfig   string
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
	transcribeCmd.Flags().StringVarP(&tBackend, "backend", "b", "", "transcription backend (whisper, deepgram, openai, mistral)")
	transcribeCmd.Flags().StringVarP(&tModel, "model", "m", "", "model name (backend-specific)")
	transcribeCmd.Flags().StringVarP(&tLanguage, "language", "l", "", "language hint (ISO 639-1)")
	transcribeCmd.Flags().StringVarP(&tOutput, "output", "o", "", "output file (default: stdout)")
	transcribeCmd.Flags().StringVarP(&tFormat, "format", "f", "text", "output format (text, json, srt, vtt)")
	transcribeCmd.Flags().BoolVarP(&tVerbose, "verbose", "v", false, "show progress and timing info")
	transcribeCmd.Flags().StringVar(&tConfig, "config", "", "config file path")
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

	if tVerbose {
		fmt.Fprintf(os.Stderr, "Using backend: %s\n", backend.Name())
	}

	opts := transcribe.TranscribeOpts{
		Model:    tModel,
		Language: tLanguage,
		Format:   transcribe.ParseFormat(tFormat),
	}

	result, err := backend.Transcribe(ctx, audioPath, opts)
	if err != nil {
		return err
	}

	output := result.Format(opts.Format)

	if tOutput != "" {
		return os.WriteFile(tOutput, []byte(output), 0644)
	}

	fmt.Print(output)
	return nil
}

func bufferStdin() (string, error) {
	tmp, err := os.CreateTemp("", "audiotools-stdin-*")
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
