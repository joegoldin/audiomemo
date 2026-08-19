package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
)

// ttyChoice is where an interactive TUI renders. Bubbletea defaults to stdout,
// which is wrong the moment stdout is a pipe: `record | copy` would send the
// alternate-screen escape sequences to the clipboard instead of the terminal.
type ttyChoice int

const (
	ttyStdout ttyChoice = iota
	ttyDevTTY
	ttyStderr
	ttyNone
)

// chooseTTY ranks the places a TUI can render. /dev/tty outranks stderr
// because `record | copy 2>/dev/null` is an ordinary thing to type and should
// not blank the interface; stderr is the fallback for the case where there is
// no controlling terminal to open but fd 2 still points at one.
func chooseTTY(stdoutIsTTY, devTTYOpen, stderrIsTTY bool) ttyChoice {
	switch {
	case stdoutIsTTY:
		return ttyStdout
	case devTTYOpen:
		return ttyDevTTY
	case stderrIsTTY:
		return ttyStderr
	default:
		return ttyNone
	}
}

// tuiTarget carries the bubbletea options that put a TUI on the terminal, plus
// the cleanup for anything opened to get there.
type tuiTarget struct {
	// Available is false when nothing interactive could be found, which means
	// the caller must fall back to headless.
	Available bool
	// Note describes a non-default target, for a one-line warning on stderr.
	Note    string
	options []tea.ProgramOption
	tty     *os.File
}

// Options returns the program options for this target, appended to whatever
// the caller already wanted.
func (t tuiTarget) Options(extra ...tea.ProgramOption) []tea.ProgramOption {
	return append(extra, t.options...)
}

// Close releases the terminal opened for the TUI, if any.
func (t tuiTarget) Close() {
	if t.tty != nil {
		t.tty.Close()
	}
}

// resolveTUITarget finds somewhere to draw a TUI. When it settles on
// /dev/tty, that file becomes both the input and the output, so a TUI still
// responds to keys under `record < /dev/null | copy`.
func resolveTUITarget() tuiTarget {
	var tty *os.File
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		// Opened before the decision because whether it opens is part of it.
		tty, _ = os.OpenFile("/dev/tty", os.O_RDWR, 0)
	}

	switch chooseTTY(isatty.IsTerminal(os.Stdout.Fd()), tty != nil, isatty.IsTerminal(os.Stderr.Fd())) {
	case ttyStdout:
		return tuiTarget{Available: true}
	case ttyDevTTY:
		return tuiTarget{
			Available: true,
			options:   []tea.ProgramOption{tea.WithOutput(tty), tea.WithInput(tty)},
			tty:       tty,
		}
	case ttyStderr:
		return tuiTarget{
			Available: true,
			Note:      "stdout is not a terminal: drawing the interface on stderr",
			options:   []tea.ProgramOption{tea.WithOutput(os.Stderr)},
		}
	default:
		if tty != nil {
			tty.Close()
		}
		return tuiTarget{Note: "no terminal available: recording headless"}
	}
}

// warnTUITarget prints the target's note, if it has one. Notes go to stderr so
// they stay out of a captured transcript.
func warnTUITarget(t tuiTarget) {
	if t.Note != "" {
		fmt.Fprintln(os.Stderr, t.Note)
	}
}
