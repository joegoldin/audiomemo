package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/spf13/cobra"
)

const maxFilenameLen = 250 // stay under typical 255-byte filesystem limit

var transcribeLatestCmd = &cobra.Command{
	Use:   "latest [name]",
	Short: "Transcribe the most recent recording",
	Long: `Find the newest audio file in the recordings directory and transcribe it.

If a name is provided, the recording is renamed to include it
(e.g. "recording-2025-02-25T12-00-00.ogg" → "recording-2025-02-25T12-00-00-standup.ogg").
The name is truncated if the resulting filename would exceed filesystem limits.

Uses the same output directory as the record command (default: ~/Recordings).
All transcribe flags (--backend, --format, etc.) are supported.

Examples:
  transcribe latest
  transcribe latest standup
  transcribe latest "team meeting" -b deepgram
  transcribe latest -v --copy`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTranscribeLatest,
}

var audioExtensions = map[string]bool{
	".ogg":  true,
	".wav":  true,
	".flac": true,
	".mp3":  true,
	".m4a":  true,
	".webm": true,
	".opus": true,
}

func runTranscribeLatest(cmd *cobra.Command, args []string) error {
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

	outputDir := cfg.ResolveOutputDir()

	latest, err := findLatestAudio(outputDir)
	if err != nil {
		return err
	}

	// Rename the file if a name was provided.
	if len(args) > 0 && args[0] != "" {
		renamed, err := renameWithLabel(latest, args[0])
		if err != nil {
			return fmt.Errorf("failed to rename recording: %w", err)
		}
		latest = renamed
	}

	fmt.Fprintf(os.Stderr, "Transcribing %s\n", filepath.Base(latest))

	// Delegate to the transcribe command with the found file.
	return runTranscribe(cmd, []string{latest})
}

// renameWithLabel appends a sanitized label to the filename before the extension,
// truncating if the result would exceed maxFilenameLen.
func renameWithLabel(path, label string) (string, error) {
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)

	// Sanitize: replace spaces/slashes with hyphens, collapse runs.
	label = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == '\x00' {
			return '-'
		}
		if r == ' ' {
			return '-'
		}
		return r
	}, label)
	label = strings.Trim(label, "-")

	newBase := base + "-" + label
	// Truncate if needed, keeping the extension.
	if len(newBase)+len(ext) > maxFilenameLen {
		newBase = newBase[:maxFilenameLen-len(ext)]
		newBase = strings.TrimRight(newBase, "-")
	}

	newPath := filepath.Join(dir, newBase+ext)
	if newPath == path {
		return path, nil
	}
	if err := os.Rename(path, newPath); err != nil {
		return "", err
	}
	return newPath, nil
}

func findLatestAudio(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("cannot read recordings directory %s: %w", dir, err)
	}

	var latestPath string
	var latestTime int64

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !audioExtensions[ext] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if t := info.ModTime().UnixNano(); t > latestTime {
			latestTime = t
			latestPath = filepath.Join(dir, e.Name())
		}
	}

	if latestPath == "" {
		return "", fmt.Errorf("no audio files found in %s", dir)
	}
	return latestPath, nil
}
