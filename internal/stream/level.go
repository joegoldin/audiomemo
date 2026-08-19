package stream

import (
	"math"
	"time"
)

// FloorDB is the quietest reading the meter distinguishes. ffmpeg's astats
// reports digital silence as -inf, so a floor is needed either way, and -60
// is the one the recording TUI has always used.
const FloorDB = -60.0

// NormalizeLevel maps a dBFS reading onto [0, 1] against FloorDB. NaN and
// anything below the floor read as silence; anything at or above full scale
// reads as 1.
func NormalizeLevel(db float64) float64 {
	if math.IsNaN(db) || db <= FloorDB {
		return 0
	}
	if db >= 0 {
		return 1
	}
	return (db - FloorDB) / -FloorDB
}

// ClampDB finite-ises a dBFS reading for the wire. See Emitter.Level for why
// this matters: encoding/json refuses ±Inf and NaN outright.
func ClampDB(db float64) float64 {
	if math.IsNaN(db) || db < FloorDB {
		return FloorDB
	}
	if db > 0 {
		return 0
	}
	return db
}

// LevelThrottle coalesces ffmpeg's ~100 Hz RMS lines down to one reading per
// interval, keeping the loudest in each window. Keeping the peak rather than
// the last reading is what stops a meter from missing transients.
type LevelThrottle struct {
	interval time.Duration
	now      func() time.Time

	opened   time.Time
	peak     float64
	havePeak bool
	started  bool
}

// NewLevelThrottle returns a throttle emitting at most one reading per interval.
func NewLevelThrottle(interval time.Duration) *LevelThrottle {
	return newLevelThrottle(interval, time.Now)
}

func newLevelThrottle(interval time.Duration, now func() time.Time) *LevelThrottle {
	return &LevelThrottle{interval: interval, now: now}
}

// Push feeds one dBFS reading. It returns the value to emit and true when the
// current window has closed, and false while the window is still filling.
func (t *LevelThrottle) Push(db float64) (float64, bool) {
	now := t.now()
	if !t.started {
		t.started = true
		t.opened = now
		return db, true
	}
	if !t.havePeak || db > t.peak {
		t.peak = db
		t.havePeak = true
	}
	if now.Sub(t.opened) < t.interval {
		return 0, false
	}
	peak := t.peak
	t.opened = now
	t.peak = 0
	t.havePeak = false
	return peak, true
}
