package record

import "time"

// DefaultSilenceThreshold is the RMS level, in dBFS, at or below which a
// reading counts as silence. Room noise on a typical mic sits near -55 dBFS
// and speech near -25, so -40 separates them with room on both sides.
const DefaultSilenceThreshold = -40.0

// SilenceWatcher decides when a recording has gone quiet long enough to stop
// on its own. It is fed every RMS reading ffmpeg prints and answers one
// question: has the room been quiet, since the last thing it heard, for
// longer than the limit?
//
// The clock starts at the first reading above the threshold rather than at
// t=0, so --max-silence 2s does not end the recording while the speaker is
// still reaching for the mic. A recording that never rises above the
// threshold therefore never trips this; --max-duration is the backstop.
type SilenceWatcher struct {
	threshold float64
	limit     time.Duration

	lastLoud time.Time // zero until the first reading above the threshold
	fired    bool
}

// NewSilenceWatcher returns nil when limit is not positive, which is how the
// feature stays off: every method is nil-safe, so callers hold the result
// without testing it.
func NewSilenceWatcher(threshold float64, limit time.Duration) *SilenceWatcher {
	if limit <= 0 {
		return nil
	}
	return &SilenceWatcher{threshold: threshold, limit: limit}
}

// Push feeds one RMS reading and reports whether the silence limit has just
// been exceeded. It returns true at most once, so the caller can treat it
// directly as "stop now" without guarding against repeats.
func (w *SilenceWatcher) Push(db float64, now time.Time) bool {
	if w == nil || w.fired {
		return false
	}
	if db > w.threshold {
		w.lastLoud = now
		return false
	}
	if w.lastLoud.IsZero() || now.Sub(w.lastLoud) < w.limit {
		return false
	}
	w.fired = true
	return true
}
