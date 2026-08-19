package cmd

import (
	"fmt"
	"strings"
)

// printMode says what `record` writes to stdout. The historical behaviour is
// printPath, which supports `transcribe $(record)`; printText is what makes
// `record | copy` land the transcript in the clipboard rather than a filename.
type printMode string

const (
	printAuto printMode = "auto"
	printPath printMode = "path"
	printText printMode = "text"
	printBoth printMode = "both"
	printNone printMode = "none"
)

// parsePrintMode validates the --print value. An empty string is the unset
// flag, which is auto.
func parsePrintMode(s string) (printMode, error) {
	switch printMode(strings.ToLower(s)) {
	case "", printAuto:
		return printAuto, nil
	case printPath:
		return printPath, nil
	case printText:
		return printText, nil
	case printBoth:
		return printBoth, nil
	case printNone:
		return printNone, nil
	}
	return "", fmt.Errorf("invalid --print %q: want auto, path, text, both, or none", s)
}

// resolvePrintMode turns auto into a concrete choice. A human watching a
// terminal wants the path — it is the one piece of information the TUI did not
// already show them. A pipe wants the words: nobody writes `record | copy` to
// put a filename on the clipboard.
//
// Clips mode produces one recording per clip and prints a line per clip as it
// goes, so auto stays on paths there rather than interleaving transcripts.
func resolvePrintMode(mode printMode, stdoutIsTTY, clips bool) printMode {
	if mode != printAuto {
		return mode
	}
	if stdoutIsTTY || clips {
		return printPath
	}
	return printText
}

// validatePrintFlags rejects the combinations --print cannot honour. Both
// refusals are about stdout having another owner: --stream fills it with
// NDJSON, and --clips fills it with one path per clip.
func validatePrintFlags(changed bool, mode printMode, streamFlag, clips bool) error {
	if !changed {
		return nil
	}
	if streamFlag {
		return fmt.Errorf("--print cannot be combined with --stream: stdout carries NDJSON, and the transcript arrives on the final event")
	}
	if mode == printText || mode == printBoth {
		if clips {
			return fmt.Errorf("--print %s cannot be combined with --clips: each clip gets its own transcript, so stdout stays a list of paths", mode)
		}
	}
	return nil
}

// resolveStdoutText picks the transcript --print text should emit. The
// precedence matches what the run leaves on disk at <base>.txt: the batch pass
// wins when it ran and produced text, because it is the higher-quality
// diarised result, and the promoted live transcript is the fallback.
//
// An unreadable or blank transcript is reported as no transcript. Writing a
// read error to stdout would put it on the user's clipboard.
func resolveStdoutText(batchText, transcriptPath string, readFile func(string) ([]byte, error)) string {
	if text := strings.TrimSpace(batchText); text != "" {
		return text
	}
	data, err := readFile(transcriptPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
