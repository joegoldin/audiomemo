// Package stream defines the newline-delimited JSON that `record --stream`
// writes to stdout: one JSON object per line, emitted as it happens.
//
// The wire carries measurements, not a rendering. Levels are raw readings on
// two scales and text is exactly what the backend produced; smoothing, colour,
// and layout belong to the consumer, which knows its own terminal width.
package stream

// Event type discriminators.
const (
	TypeStart   = "start"
	TypeLevel   = "level"
	TypePartial = "partial"
	TypeCommit  = "commit"
	TypeFinal   = "final"
	TypeError   = "error"
	TypeEnd     = "end"
)

// StartEvent.Mode values. Mode answers one question: will partials arrive?
const (
	ModeLive  = "live"  // realtime backend connected; partial and commit will follow
	ModeBatch = "batch" // no partials, but a batch pass will produce one final
	ModeNone  = "none"  // no transcript will be produced at all
)

// FinalEvent.Source values.
const (
	SourceLive  = "live"
	SourceBatch = "batch"
)

// ErrorEvent.Scope values.
const (
	ScopeRecord     = "record"
	ScopeStream     = "stream"
	ScopeTranscribe = "transcribe"
	ScopeConfig     = "config"
)

// EndEvent.Reason values.
const (
	ReasonStopped = "stopped" // ffmpeg exited on its own (duration elapsed, device gone)
	ReasonSignal  = "signal"  // SIGINT or SIGTERM; a deliberate stop
	ReasonError   = "error"   // the run failed
)

// header is embedded in every event. Anonymous with no tag of its own, so its
// fields are promoted into the top-level JSON object.
type header struct {
	Type string `json:"type"`
	T    int64  `json:"t"` // milliseconds since the stream opened
}

// StartEvent is emitted once, after the recorder is running and the realtime
// backend has either connected or failed. Mode is therefore a fact.
type StartEvent struct {
	header
	Device      string   `json:"device"`
	DeviceLabel string   `json:"device_label"`
	Devices     []string `json:"devices"`
	Path        string   `json:"path"`
	Format      string   `json:"format"`
	SampleRate  int      `json:"sample_rate"`
	Channels    int      `json:"channels"`
	Mode        string   `json:"mode"`
	Backend     string   `json:"backend,omitempty"`
}

// LevelEvent carries one mic reading on both scales: RMS normalised onto
// [0,1] for a meter, and the dBFS the meter was derived from for a readout.
type LevelEvent struct {
	header
	RMS float64 `json:"rms"`
	DB  float64 `json:"db"`
}

// TextEvent backs both partial and commit. A partial replaces the previous
// partial; a commit is appended and will not change.
type TextEvent struct {
	header
	Text string `json:"text"`
}

// FinalEvent is the finished transcript. Source says where it came from: the
// realtime session, or the higher-quality batch pass that ran afterwards.
type FinalEvent struct {
	header
	Text           string `json:"text"`
	Path           string `json:"path"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Backend        string `json:"backend,omitempty"`
	Source         string `json:"source"`
}

// ErrorEvent replaces the stderr warnings the non-stream paths print. Fatal
// distinguishes "recording continued without this" from "the run is over".
type ErrorEvent struct {
	header
	Scope   string `json:"scope"`
	Fatal   bool   `json:"fatal"`
	Message string `json:"message"`
}

// EndEvent is always the last line. Reaching EOF without one means the
// producer died rather than finished.
type EndEvent struct {
	header
	Reason   string `json:"reason"`
	Path     string `json:"path,omitempty"`
	ExitCode int    `json:"exit_code"`
}
