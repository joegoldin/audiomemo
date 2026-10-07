package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/transcribe"
	"github.com/joegoldin/audiomemo/internal/tui"
	"github.com/joegoldin/audiomemo/internal/voicememo"
	"github.com/spf13/cobra"
)

var transcribeVoiceMemoCmd = &cobra.Command{
	Use:     "voice-memo [name]",
	Aliases: []string{"vm", "voice-memos"},
	Short:   "Transcribe a macOS Voice Memo by name or pick one interactively",
	Long: `Transcribe a downloaded recording from the macOS Voice Memos library.

Match a display name or filename (case-insensitive; unique substrings work).
Without a name, or with multiple matches, open a searchable picker.
Type to filter, Enter to select, or Esc to cancel.

All transcribe flags are supported. Transcripts are saved in the configured
recordings directory (default: ~/Recordings), never in Apple's library.
Use --output to choose a different destination.

Examples:
  transcribe vm
  transcribe vm "Team meeting" --backend nemo
  transcribe voice-memo meeting --format srt --output meeting.srt`,
	RunE: runTranscribeVoiceMemo,
}

func runTranscribeVoiceMemo(cmd *cobra.Command, args []string) error {
	recordings, err := voicememo.List(cmd.Context())
	if err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(args, " "))
	if len(args) > 0 {
		if query == "" {
			return fmt.Errorf("Voice Memo name must not be empty")
		}
		recordings = voicememo.Match(recordings, query)
		if len(recordings) == 0 {
			return fmt.Errorf("no downloaded Voice Memo matches %q", query)
		}
	}
	var selected *voicememo.Recording
	if query != "" && len(recordings) == 1 {
		selected = &recordings[0]
	} else {
		target := resolveTUITarget()
		defer target.Close()
		if !target.Available {
			if query != "" {
				return fmt.Errorf("%d Voice Memos match %q; use an exact, unique name or filename, or run in a terminal to choose", len(recordings), query)
			}
			return fmt.Errorf("Voice Memo picker requires a terminal; pass a recording name or filename")
		}
		selected, err = tui.RunVoiceMemoPicker(recordings, target.Options()...)
		if err != nil {
			return err
		}
		if selected == nil {
			return nil
		}
	}
	savePath, err := voiceMemoTranscriptPath(selected.Path)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Transcribing %s\n", selected.Name)
	return runTranscribeFile(cmd, selected.Path, savePath)
}

func voiceMemoTranscriptPath(audioPath string) (string, error) {
	output := tOutput
	if output == "" {
		var cfg *config.Config
		var err error
		if tConfig != "" {
			cfg, err = config.LoadFrom(tConfig)
		} else {
			cfg, err = config.Load()
		}
		if err != nil {
			return "", fmt.Errorf("failed to load config: %w", err)
		}
		output = filepath.Join(cfg.ResolveOutputDir(), transcriptPathFor(filepath.Base(audioPath), transcribe.ParseFormat(tFormat)))
	}
	path, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}
	// Check the destination before creating directories, including symlinks
	// and paths whose final components do not exist yet.
	realPath, err := resolveVoiceMemoOutput(path)
	if err != nil {
		return "", err
	}
	libraryDir, err := filepath.EvalSymlinks(filepath.Dir(audioPath))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(libraryDir, realPath)
	if err != nil {
		return "", err
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("transcript destination must be outside the Voice Memos library")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("create transcript directory: %w", err)
	}
	if tOutput != "" {
		return "", nil // runTranscribeFile writes the explicit --output destination.
	}
	return path, nil
}

func resolveVoiceMemoOutput(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) || filepath.Dir(path) == path {
		return "", err
	}
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		return resolveVoiceMemoOutput(target)
	}
	parent, err := resolveVoiceMemoOutput(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
