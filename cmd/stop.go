package cmd

import (
	"fmt"
	"time"

	"github.com/joegoldin/audiomemo/internal/record"
)

// parseFlagDuration parses a Go duration for the named flag, rejecting the
// negative values time.ParseDuration accepts. The flag name is in the error
// because the same function serves three flags.
func parseFlagDuration(flag, value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: want a duration like 30s, 5m, or 1h30m", flag, value)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid %s %q: duration must be positive", flag, value)
	}
	return d, nil
}

// resolveMaxDuration merges --max-duration with the older --duration spelling,
// which is kept as a hidden alias. --duration was declared but never read, so
// nothing can depend on its behaviour — only on the name not erroring.
func resolveMaxDuration(maxDuration, alias string) (time.Duration, error) {
	if maxDuration != "" {
		return parseFlagDuration("--max-duration", maxDuration)
	}
	return parseFlagDuration("--duration", alias)
}

func parseMaxSilence(value string) (time.Duration, error) {
	return parseFlagDuration("--max-silence", value)
}

// headlessStopHint describes how a headless recording will end. Without it a
// --max-duration run looks identical to one that waits forever.
func headlessStopHint(maxDuration, maxSilence time.Duration) string {
	switch {
	case maxDuration > 0 && maxSilence > 0:
		return fmt.Sprintf("stopping after %s or %s of silence, or Ctrl+C", maxDuration, maxSilence)
	case maxDuration > 0:
		return fmt.Sprintf("stopping after %s, or Ctrl+C", maxDuration)
	case maxSilence > 0:
		return fmt.Sprintf("stopping after %s of silence, or Ctrl+C", maxSilence)
	default:
		return "Ctrl+C to stop"
	}
}

// wantsStdoutText reports whether the resolved --print mode puts transcript
// text on stdout.
func wantsStdoutText(mode printMode) bool {
	return mode == printText || mode == printBoth
}

// batchNeededForText reports whether the batch pass has to run to satisfy
// --print. Asking for text when live transcription is not running leaves the
// batch pass as the only way to produce any, so it runs rather than emitting
// nothing: the request itself is the consent. With live transcription running
// the live transcript already answers, and Q or -t still upgrade it.
func batchNeededForText(mode printMode, liveActive, alreadyTranscribing bool) bool {
	return alreadyTranscribing || (wantsStdoutText(mode) && !liveActive)
}

// stopConditions is the parsed form of the flags that end a recording without
// the user pressing anything. It travels as one value so the recording paths
// take a parameter rather than three.
type stopConditions struct {
	MaxDuration time.Duration
	MaxSilence  time.Duration
	Threshold   float64
}

// apply copies the stop conditions onto recorder options.
func (s stopConditions) apply(opts record.RecordOpts) record.RecordOpts {
	opts.MaxDuration = s.MaxDuration
	opts.MaxSilence = s.MaxSilence
	opts.SilenceThreshold = s.Threshold
	return opts
}

// hint describes the stops for the headless status line.
func (s stopConditions) hint() string {
	return headlessStopHint(s.MaxDuration, s.MaxSilence)
}
