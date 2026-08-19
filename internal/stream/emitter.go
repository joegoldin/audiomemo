package stream

import (
	"encoding/json"
	"io"
	"math"
	"sync"
	"time"
)

// FloorDB is the quietest reading the meter distinguishes. Task 2 moves this
// to level.go alongside the normalisation it belongs with.
const FloorDB = -60.0

// Emitter serialises events onto one writer. Levels arrive on ffmpeg's stderr
// goroutine and text arrives on the websocket goroutine, so the mutex is what
// keeps lines whole.
type Emitter struct {
	mu  sync.Mutex
	enc *json.Encoder

	start time.Time
	now   func() time.Time
}

// NewEmitter returns an Emitter writing NDJSON to w.
func NewEmitter(w io.Writer) *Emitter { return newEmitter(w, time.Now) }

func newEmitter(w io.Writer, now func() time.Time) *Emitter {
	enc := json.NewEncoder(w)
	// Transcripts contain apostrophes and angle brackets; escaping them would
	// be visible in the consumer's UI.
	enc.SetEscapeHTML(false)
	return &Emitter{enc: enc, start: now(), now: now}
}

func (e *Emitter) header(t string) header {
	return header{Type: t, T: e.now().Sub(e.start).Milliseconds()}
}

// emit writes one line. json.Encoder.Encode appends the newline itself, which
// is exactly the NDJSON framing. A write error means the consumer is gone;
// there is nowhere useful to report that, so it is dropped.
func (e *Emitter) emit(v any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.enc.Encode(v)
}

func (e *Emitter) Start(ev StartEvent) {
	ev.header = e.header(TypeStart)
	if ev.Devices == nil {
		ev.Devices = []string{}
	}
	e.emit(ev)
}

// Level clamps both scales before encoding. encoding/json returns an
// UnsupportedValueError for ±Inf and NaN, and a refused encode would drop the
// whole line rather than one field.
func (e *Emitter) Level(rms, db float64) {
	e.emit(LevelEvent{header: e.header(TypeLevel), RMS: finite(rms, 0, 1), DB: finite(db, FloorDB, 0)})
}

func (e *Emitter) Partial(text string) {
	e.emit(TextEvent{header: e.header(TypePartial), Text: text})
}

func (e *Emitter) Commit(text string) {
	e.emit(TextEvent{header: e.header(TypeCommit), Text: text})
}

func (e *Emitter) Final(ev FinalEvent) {
	ev.header = e.header(TypeFinal)
	e.emit(ev)
}

func (e *Emitter) Error(scope string, fatal bool, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	e.emit(ErrorEvent{header: e.header(TypeError), Scope: scope, Fatal: fatal, Message: msg})
}

func (e *Emitter) End(ev EndEvent) {
	ev.header = e.header(TypeEnd)
	e.emit(ev)
}

// finite maps NaN to lo and clamps everything else into [lo, hi], so +Inf
// saturates at hi and -Inf at lo.
func finite(v, lo, hi float64) float64 {
	if math.IsNaN(v) || v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
