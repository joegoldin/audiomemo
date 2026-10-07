package cmd

import (
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/transcribe"
	"github.com/joegoldin/audiomemo/internal/tui"
	"github.com/joegoldin/audiomemo/internal/voicememo"
	"github.com/spf13/cobra"
)

var transcribeVoiceMemoCmd = &cobra.Command{
	Use:     "voice-memo [name | latest | list [page]]",
	Aliases: []string{"vm", "voice-memos"},
	Short:   "Transcribe a macOS Voice Memo by name or pick one interactively",
	Long: `Transcribe a downloaded recording from the macOS Voice Memos library.

Match a display name or filename, case-insensitively: exact matches first,
then substrings, then fuzzy matches (characters in order). Multiple matches
select the newest recording in the best matching group.
Use latest to transcribe the newest downloaded memo, or list [page] to show
10 memos per page, newest first, without transcribing. Pages start at 1;
omitting the page shows page 1. Both keywords are case-insensitive.
Use -- before a name to search for a literal name such as latest or list.
Without a name, open a searchable picker. Enter selects; Esc cancels.

All transcribe flags are supported. Transcripts are saved in the configured
recordings directory (default: ~/Recordings), never in Apple's library.
Use --output to choose a different destination.

Examples:
  transcribe vm
  transcribe vm latest --backend nemo
  transcribe vm list
  transcribe vm list 2
  transcribe vm -- latest
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
	latest := false
	if len(args) > 0 && cmd.ArgsLenAtDash() != 0 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "latest":
			latest = len(args) == 1
		case "list":
			if len(args) > 2 {
				return fmt.Errorf("usage: transcribe vm list [page]")
			}
			page := 1
			if len(args) == 2 {
				page, err = strconv.Atoi(args[1])
				if err != nil || page < 1 {
					return fmt.Errorf("Voice Memo page must be a positive integer")
				}
			}
			const pageSize = 10
			pages := (len(recordings)-1)/pageSize + 1
			// Check the page before multiplying so huge inputs cannot overflow.
			if page > pages {
				return fmt.Errorf("Voice Memo page %d is out of range; choose 1–%d", page, pages)
			}
			start := (page - 1) * pageSize
			recordings = recordings[start:min(start+pageSize, len(recordings))]
			output := cmd.OutOrStdout()
			terminal := false
			if file, ok := output.(*os.File); ok {
				terminal = isatty.IsTerminal(file.Fd())
			}
			return writeVoiceMemoList(output, recordings, terminal)
		}
	}
	if len(args) > 0 && !latest {
		if query == "" {
			return fmt.Errorf("Voice Memo name must not be empty")
		}
		recordings = voicememo.Match(recordings, query)
		if len(recordings) == 0 {
			return fmt.Errorf("no downloaded Voice Memo matches %q", query)
		}
	}
	var selected *voicememo.Recording
	if latest || query != "" {
		// List is newest first, and Match preserves that order within each tier.
		selected = &recordings[0]
	} else {
		target := resolveTUITarget()
		defer target.Close()
		if !target.Available {
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

func writeVoiceMemoList(output io.Writer, recordings []voicememo.Recording, hyperlinks bool) error {
	w := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "DATE\tNAME\tDURATION\tFILE"); err != nil {
		return err
	}
	for _, recording := range recordings {
		link := (&url.URL{Scheme: "file", Path: recording.Path}).String()
		if hyperlinks {
			link = ansi.SetHyperlink(link) + voiceMemoListText(filepath.Base(recording.Path)) + ansi.ResetHyperlink()
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", recording.Date.Local().Format("2006-01-02 15:04:05 MST"), voiceMemoListText(recording.Name), voiceMemoDuration(recording.Duration), link); err != nil {
			return err
		}
	}
	return w.Flush()
}

func voiceMemoListText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}

func voiceMemoDuration(duration *float64) string {
	if duration == nil || math.IsNaN(*duration) || math.IsInf(*duration, 0) || *duration < 0 {
		return "—"
	}
	seconds := int64(math.Round(*duration))
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
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
