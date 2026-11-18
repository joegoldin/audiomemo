// Package sessiontypes defines the data shared by the desktop control socket,
// meeting manifests and the two native desktop clients. Existing record --stream
// events have a separate compatibility contract.
package sessiontypes

import "context"

const Version = 1
const MaxFrame = 1 << 20
const QuickPCMBytes = 32_000 * 300 // five minutes of 16 kHz mono s16 audio

type Source struct {
	ID     string `json:"id"`
	Device string `json:"device"`
	Alias  string `json:"alias,omitempty"`
	Role   string `json:"role"`
	Reason string `json:"reason"`
}
type Gap struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
}
type Track struct {
	Source
	Files    []string `json:"files"`
	Rate     int      `json:"rate"`
	Channels int      `json:"channels"`
	OffsetMS int64    `json:"offset_ms"`
	Gaps     []Gap    `json:"gaps,omitempty"`
	Error    string   `json:"error,omitempty"`
}
type Manifest struct {
	Version    int     `json:"version"`
	ID         string  `json:"id"`
	Group      string  `json:"group,omitempty"`
	State      string  `json:"state"`
	StartedUTC string  `json:"started_utc"`
	DurationMS int64   `json:"duration_ms"`
	Tracks     []Track `json:"tracks"`
	Mix        string  `json:"mix,omitempty"`
	Transcript string  `json:"transcript,omitempty"`
	Error      string  `json:"error,omitempty"`
}
type Preview struct {
	Session   string `json:"session"`
	Stable    string `json:"stable"`
	Tentative string `json:"tentative"`
}
type Status struct {
	Capture    string `json:"capture"`
	Session    string `json:"session,omitempty"`
	StartedUTC string `json:"started_utc,omitempty"`
	Job        string `json:"job,omitempty"`
	JobState   string `json:"job_state,omitempty"`
	Warning    string `json:"warning,omitempty"`
}
type Request struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
	Device  string `json:"device,omitempty"`
	Bundle  string `json:"bundle,omitempty"`
	Wait    bool   `json:"wait,omitempty"`
	Print   string `json:"print,omitempty"`
	Session string `json:"session,omitempty"`
}
type Reply struct {
	Version int    `json:"version"`
	Status  Status `json:"status"`
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	Text    string `json:"text,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Controller executes a local control request. A canceled context abandons the
// wait, not a previously admitted recording or its queued finalization.
type Controller interface {
	Do(context.Context, Request) Reply
}

// Desktop is called by the daemon. Run owns the native UI thread and returns
// on shutdown. All other methods must be safe from worker goroutines.
type Desktop interface {
	Run(context.Context, Controller) error
	Update(Status)
	Caption(Preview)
	Target() string
	Deliver(context.Context, string, string, bool) error // text, original target, paste requested
}

// PCM is a best-effort preview of a track, not the authoritative recording.
type PCM struct {
	Track    string
	StartMS  int64
	Rate     int
	Channels int
	S16LE    []byte
}
type Meeting interface {
	Stop(context.Context) (string, error)
	Bundle() string
}
type Quick interface {
	Stop(context.Context) (string, error)
	Cancel()
}
