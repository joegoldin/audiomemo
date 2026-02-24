package transcribe

import (
	"context"
	"fmt"
)

type OutputFormat string

const (
	FormatText OutputFormat = "text"
	FormatJSON OutputFormat = "json"
	FormatSRT  OutputFormat = "srt"
	FormatVTT  OutputFormat = "vtt"
)

func ParseFormat(s string) OutputFormat {
	switch s {
	case "json":
		return FormatJSON
	case "srt":
		return FormatSRT
	case "vtt":
		return FormatVTT
	default:
		return FormatText
	}
}

type TranscribeOpts struct {
	Model       string
	Language    string
	Format      OutputFormat
	Verbose     bool
	Diarize     bool
	SmartFormat bool
	Punctuate   bool
	FillerWords bool
	Numerals    bool
}

type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error)
	Name() string
}

// validateOpts checks that the requested transcription options are supported by the backend.
// Returns an error if an unsupported option is requested.
func validateOpts(backendName string, opts TranscribeOpts, supportsDiarize, supportsSmartFormat, supportsPunctuate, supportsFillerWords, supportsNumerals bool) error {
	if opts.Diarize && !supportsDiarize {
		return fmt.Errorf("%s does not support --diarize", backendName)
	}
	if opts.SmartFormat && !supportsSmartFormat {
		return fmt.Errorf("%s does not support --smart-format", backendName)
	}
	if opts.Punctuate && !supportsPunctuate {
		return fmt.Errorf("%s does not support --punctuate", backendName)
	}
	if opts.FillerWords && !supportsFillerWords {
		return fmt.Errorf("%s does not support --filler-words", backendName)
	}
	if opts.Numerals && !supportsNumerals {
		return fmt.Errorf("%s does not support --numerals", backendName)
	}
	return nil
}
