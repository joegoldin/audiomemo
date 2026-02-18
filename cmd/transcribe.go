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
	tCopy     bool
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
	transcribeCmd.Flags().StringVarP(&tBackend, "backend", "b", "", "transcription backend (whisper, whisper-cpp, whisperx, ffmpeg-whisper, deepgram, openai, mistral)")
	transcribeCmd.Flags().StringVarP(&tModel, "model", "m", "", "model name (backend-specific)")
	transcribeCmd.Flags().StringVarP(&tLanguage, "language", "l", "", "language hint (ISO 639-1)")
	transcribeCmd.Flags().StringVarP(&tOutput, "output", "o", "", "output file (default: stdout)")
	transcribeCmd.Flags().StringVarP(&tFormat, "format", "f", "text", "output format (text, json, srt, vtt)")
	transcribeCmd.Flags().BoolVarP(&tVerbose, "verbose", "v", false, "show progress and timing info")
	transcribeCmd.Flags().BoolVarP(&tCopy, "copy", "C", false, "copy output to clipboard")
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

	opts := transcribe.TranscribeOpts{
		Model:    tModel,
		Language: tLanguage,
		Format:   transcribe.ParseFormat(tFormat),
		Verbose:  tVerbose,
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
