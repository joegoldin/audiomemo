package transcribe

import "context"

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
	Model    string
	Language string
	Format   OutputFormat
	Verbose  bool
}

type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error)
	Name() string
}
