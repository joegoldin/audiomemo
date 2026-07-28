# Live Viewer Transcription Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the big waveform animation with a transcript-first recording screen where a single-cell VU cursor (height + color track mic level) sits at the text insertion point; make live transcription always-on, with `q` promoting the live transcript to `<base>.txt` and `Q`/`-t` running batch retranscription — in both single-recording and clips mode.

**Architecture:** The Bubble Tea `Model` in `internal/tui/model.go` drops its `Animation` and renders one unified layout (header / mic / out / separator / transcript viewport / separator / keys) for all modes. A new `VUMeter` (attack/decay smoothing) plus `renderVUCursor` in `internal/tui/vu.go` produce a styled block-rune cell that `wrapTranscript` appends at the end of the text, reserving one cell so it never wraps alone. `cmd/record.go` creates the streamer whenever an ElevenLabs key exists, promotes `<base>-live.txt` → `<base>.txt` after every recording, and wires a fresh streamer per clip in clips mode.

**Tech Stack:** Go, Bubble Tea (`bubbletea`), Lip Gloss, Bubbles viewport, gorilla/websocket (existing). No new dependencies.

**Spec:** `docs/plans/2026-07-28-live-viewer-transcription-mode-design.md`

## Global Constraints

- No new module dependencies; the repo vendors deps (`vendor/`) — do not run `go mod tidy`/`go mod vendor`.
- Colors exactly: green `#22c55e`, yellow `#eab308`, red `#ef4444`, dim `#666666`, muted-cursor gray `#555555`, info `#a1a1aa`, amber `#f59e0b`.
- VU cursor color thresholds: green < 0.6, yellow < 0.85, red ≥ 0.85. Smoothing: attack `0.5`, decay `0.15`.
- Block runes: `▁▂▃▄▅▆▇█` (from existing `heightBlocks`); silence floors at `▁` — the cursor is never invisible.
- Flag names must not change (`-t/--transcribe` etc.) — integration tests assert them in `record --help`.
- Every commit must build (`go build ./...`) and pass `go test ./...`.
- Run all commands from the repo root `/home/joe/Development/audiomemo`.

## File Structure

- `internal/tui/vu.go` — gains `VUMeter`, `vuCursorRune`, `renderVUCursor` (Task 1); gains `heightBlocks` + wave color styles when `animation.go` is deleted (Task 6).
- `internal/tui/vu_test.go` — new tests for the above.
- `internal/tui/transcript.go` — `wrapTranscript` gains a `cursor` param with one-cell reservation; `TranscriptViewport` gains a `cursor` field + `SetCursor`.
- `internal/tui/transcript_test.go` — updated call sites + new cursor-placement tests.
- `internal/tui/model.go` — unified transcript-first layout; `Animation` removed; `StartFunc` returns streamer + note; `SetStreamNote`.
- `internal/tui/model_test.go` — new view/behavior tests.
- `internal/tui/animation.go`, `internal/tui/animation_test.go` — deleted (Task 6).
- `cmd/record.go` — always-live streamer, `promoteLiveTranscript`, clips streamer wiring, help text.
- `cmd/record_test.go` — new tests for `promoteLiveTranscript`.
- `README.md` — waveform/`-t` copy updates (Task 6).

---

### Task 1: VUMeter and VU cursor primitives

**Files:**
- Modify: `internal/tui/vu.go`
- Test: `internal/tui/vu_test.go`

**Interfaces:**
- Consumes: existing package-level `heightBlocks`, `waveGreen`, `waveYellow`, `waveRed` (still defined in `internal/tui/animation.go` until Task 6 — same package, no import needed).
- Produces: `type VUMeter struct` with methods `Push(level float64)` and `Level() float64`; `func vuCursorRune(level float64) rune`; `func renderVUCursor(level float64, paused bool) string` (returns a styled single-cell string). Task 2 consumes the styled string opaquely; Task 3 calls `m.vu.Push`, `m.vu.Level`, `renderVUCursor`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/vu_test.go`:

```go
func TestVUMeterAttackDecay(t *testing.T) {
	var v VUMeter
	// Attack: level jumps to 1.0, smoothed moves halfway.
	v.Push(1.0)
	if l := v.Level(); l < 0.49 || l > 0.51 {
		t.Errorf("expected ~0.5 after attack push, got %f", l)
	}
	// Decay: level drops to 0, smoothed falls slowly (0.5 - 0.5*0.15 = 0.425).
	v.Push(0.0)
	if l := v.Level(); l < 0.42 || l > 0.43 {
		t.Errorf("expected ~0.425 after decay push, got %f", l)
	}
}

func TestVUMeterClamps(t *testing.T) {
	var v VUMeter
	for i := 0; i < 100; i++ {
		v.Push(5.0)
	}
	if l := v.Level(); l > 1.0 {
		t.Errorf("expected level clamped to 1.0, got %f", l)
	}
	for i := 0; i < 1000; i++ {
		v.Push(-5.0)
	}
	if l := v.Level(); l < 0 {
		t.Errorf("expected level clamped to 0, got %f", l)
	}
}

func TestVUCursorRune(t *testing.T) {
	if r := vuCursorRune(0.0); r != '▁' {
		t.Errorf("silence should floor at ▁, got %q", r)
	}
	if r := vuCursorRune(1.0); r != '█' {
		t.Errorf("full level should be █, got %q", r)
	}
	// Monotonic: higher level never yields a shorter block.
	prev := vuCursorRune(0.0)
	for l := 0.0; l <= 1.0; l += 0.05 {
		r := vuCursorRune(l)
		if r < prev {
			t.Errorf("cursor rune not monotonic at level %f: %q < %q", l, r, prev)
		}
		prev = r
	}
}

func TestRenderVUCursorColors(t *testing.T) {
	if got, want := renderVUCursor(0.0, false), waveGreen.Render("▁"); got != want {
		t.Errorf("low level: got %q want %q", got, want)
	}
	if got, want := renderVUCursor(0.7, false), waveYellow.Render(string(vuCursorRune(0.7))); got != want {
		t.Errorf("mid level: got %q want %q", got, want)
	}
	if got, want := renderVUCursor(1.0, false), waveRed.Render("█"); got != want {
		t.Errorf("peak level: got %q want %q", got, want)
	}
}

func TestRenderVUCursorPaused(t *testing.T) {
	got := renderVUCursor(0.9, true)
	want := vuCursorMutedStyle.Render("▁")
	if got != want {
		t.Errorf("paused cursor: got %q want %q", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestVU|TestRenderVU' -v`
Expected: FAIL — compile errors: `undefined: VUMeter`, `undefined: vuCursorRune`, `undefined: renderVUCursor`, `undefined: vuCursorMutedStyle`.

- [ ] **Step 3: Implement in `internal/tui/vu.go`**

Add `"math"` to the imports and append:

```go
var vuCursorMutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))

// VUMeter smooths raw 0..1 levels with fast attack and slow decay — the same
// feel the old waveform's dB readout had.
type VUMeter struct {
	smoothed float64
}

// Push feeds one raw level sample (0..1) into the meter.
func (v *VUMeter) Push(level float64) {
	diff := level - v.smoothed
	if diff > 0 {
		v.smoothed += diff * 0.5
	} else {
		v.smoothed += diff * 0.15
	}
	v.smoothed = math.Max(0, math.Min(1, v.smoothed))
}

// Level returns the current smoothed level (0..1).
func (v *VUMeter) Level() float64 {
	return v.smoothed
}

// vuCursorRune maps a smoothed 0..1 level to a block rune. Silence floors at
// ▁ so the cursor never disappears.
func vuCursorRune(level float64) rune {
	idx := 1 + int(math.Round(level*7))
	if idx < 1 {
		idx = 1
	}
	if idx > 8 {
		idx = 8
	}
	return heightBlocks[idx]
}

// renderVUCursor renders the single-cell VU cursor: a block rune whose height
// and color track the mic level. When paused (muted, READY, or saved) it is a
// static dim-gray ▁.
func renderVUCursor(level float64, paused bool) string {
	if paused {
		return vuCursorMutedStyle.Render("▁")
	}
	r := string(vuCursorRune(level))
	switch {
	case level >= 0.85:
		return waveRed.Render(r)
	case level >= 0.6:
		return waveYellow.Render(r)
	default:
		return waveGreen.Render(r)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS (all package tests, including existing animation/transcript tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/vu.go internal/tui/vu_test.go
git commit -m "feat(tui): add VUMeter smoothing and single-cell VU cursor renderer"
```

---

### Task 2: Cursor-aware transcript wrapping

**Files:**
- Modify: `internal/tui/transcript.go`
- Test: `internal/tui/transcript_test.go`

**Interfaces:**
- Consumes: a styled single-cell cursor string (opaque; tests can use `renderVUCursor(0.5, false)` or any 1-wide string like `"▅"`).
- Produces: `wrapTranscript(committed, partial string, width int, cursor string) string` — cursor appended directly after the last word, one cell reserved on the final line so the cursor never wraps alone; empty transcript returns just the cursor. `(*TranscriptViewport).SetCursor(cursor string)` — stores the cursor, rebuilds content, keeps bottom pinned while auto-scrolling; no-ops when unchanged. Task 3 calls `SetCursor` every tick.

- [ ] **Step 1: Update existing call sites in tests**

In `internal/tui/transcript_test.go`, add the `""` cursor argument to every existing direct `wrapTranscript(...)` call (4 sites: `TestTranscriptViewportWordWrap`, `TestWrapTranscriptWrapsPartialOverflow`, `TestWrapTranscriptDimsPartialWords`, `TestWrapTranscriptHandlesOnlyPartial`), e.g.:

```go
	wrapped := wrapTranscript("one two three four five six", "", 20, "")
```

- [ ] **Step 2: Write the failing tests**

Append to `internal/tui/transcript_test.go`:

```go
func TestWrapTranscriptAppendsCursor(t *testing.T) {
	cursor := "▅"
	out := wrapTranscript("done", "typing", 80, cursor)
	if !strings.HasSuffix(out, cursor) {
		t.Errorf("expected output to end with cursor, got: %q", out)
	}
	lines := strings.Split(out, "\n")
	last := lines[len(lines)-1]
	if last == cursor {
		t.Errorf("cursor must not sit alone on its own line: %q", out)
	}
}

func TestWrapTranscriptCursorOnEmpty(t *testing.T) {
	cursor := "▁"
	if out := wrapTranscript("", "", 80, cursor); out != cursor {
		t.Errorf("empty transcript should render just the cursor, got: %q", out)
	}
	if out := wrapTranscript("", "", 80, ""); out != "" {
		t.Errorf("empty transcript with no cursor should be empty, got: %q", out)
	}
}

func TestWrapTranscriptCursorReservesCell(t *testing.T) {
	// "one two" is exactly 7 wide. With width 7 the cursor cell doesn't fit
	// after "two", so "two"+cursor wrap to the next line together.
	cursor := "▃"
	out := wrapTranscript("one two", "", 7, cursor)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	if lines[0] != "one" {
		t.Errorf("expected first line 'one', got %q", lines[0])
	}
	if lines[1] != "two"+cursor {
		t.Errorf("expected last line 'two'+cursor, got %q", lines[1])
	}
}

func TestWrapTranscriptNoReservationWithoutCursor(t *testing.T) {
	// Without a cursor the same text fits on one line — no behavior change.
	out := wrapTranscript("one two", "", 7, "")
	if out != "one two" {
		t.Errorf("expected single line 'one two', got %q", out)
	}
}

func TestWrapTranscriptCursorAfterDimPartial(t *testing.T) {
	cursor := "▅"
	out := wrapTranscript("done", "wip", 80, cursor)
	want := "done " + transcriptDimStyle.Render("wip") + cursor
	if out != want {
		t.Errorf("expected %q, got %q", want, out)
	}
}

func TestTranscriptViewportSetCursor(t *testing.T) {
	tv := NewTranscriptViewport(80, 24)
	tv.AppendCommitted("hello")
	tv.SetCursor("▅")
	if view := tv.viewport.View(); !strings.Contains(view, "▅") {
		t.Errorf("expected viewport content to contain cursor, got: %q", view)
	}
	tv.SetCursor("█")
	if view := tv.viewport.View(); !strings.Contains(view, "█") {
		t.Errorf("expected viewport content to update cursor, got: %q", view)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestWrapTranscript|TestTranscriptViewport' -v`
Expected: FAIL — compile errors: wrong argument count for `wrapTranscript`, `undefined` method `SetCursor`.

- [ ] **Step 4: Implement in `internal/tui/transcript.go`**

Add a `cursor` field to the struct:

```go
type TranscriptViewport struct {
	viewport   viewport.Model
	committed  string // accumulated committed text
	partial    string // current partial text (shown dim)
	cursor     string // styled single-cell VU cursor at the insertion point
	autoScroll bool
	width      int
	height     int
}
```

Add `SetCursor` after `SetPartial`:

```go
// SetCursor sets the styled single-cell VU cursor appended at the text
// insertion point and rebuilds content. No-ops when the cursor is unchanged,
// so the 30fps tick only pays for a rebuild when the level actually moved.
func (t *TranscriptViewport) SetCursor(cursor string) {
	if t.cursor == cursor {
		return
	}
	t.cursor = cursor
	t.rebuildContent()
	if t.autoScroll {
		t.viewport.GotoBottom()
	}
}
```

Update `rebuildContent`:

```go
func (t *TranscriptViewport) rebuildContent() {
	t.viewport.SetContent(wrapTranscript(t.committed, t.partial, t.width, t.cursor))
}
```

Replace `wrapTranscript` with:

```go
// wrapTranscript word-wraps committed + partial together so the partial text
// continues onto new lines instead of overflowing the last committed line.
// Partial words are rendered with transcriptDimStyle; committed words are
// rendered plain. Word boundaries are spaces; words longer than width stay
// on their own line (they are not broken). The styled single-cell cursor is
// appended directly after the last word; one cell is reserved for it on the
// final line so it never wraps onto a line by itself.
func wrapTranscript(committed, partial string, width int, cursor string) string {
	if width <= 0 {
		return committed + " " + partial + cursor
	}

	type wrapWord struct {
		text string
		dim  bool
	}
	var words []wrapWord
	for _, w := range strings.Fields(committed) {
		words = append(words, wrapWord{text: w})
	}
	for _, w := range strings.Fields(partial) {
		words = append(words, wrapWord{text: w, dim: true})
	}
	if len(words) == 0 {
		return cursor
	}

	cursorW := 0
	if cursor != "" {
		cursorW = lipgloss.Width(cursor)
	}

	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		wordLen := len(w.text)
		fitLen := wordLen
		if i == len(words)-1 {
			fitLen += cursorW
		}
		render := w.text
		if w.dim {
			render = transcriptDimStyle.Render(w.text)
		}
		switch {
		case i == 0 || lineLen == 0:
			b.WriteString(render)
			lineLen = wordLen
		case lineLen+1+fitLen > width:
			b.WriteByte('\n')
			b.WriteString(render)
			lineLen = wordLen
		default:
			b.WriteByte(' ')
			b.WriteString(render)
			lineLen += 1 + wordLen
		}
	}
	b.WriteString(cursor)
	return b.String()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/transcript.go internal/tui/transcript_test.go
git commit -m "feat(tui): append VU cursor at transcript insertion point with cell reservation"
```

---

### Task 3: Transcript-first Model rework

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `cmd/record.go` (call-site shims only — full rework in Tasks 4–5)
- Test: `internal/tui/model_test.go` (new file)

**Interfaces:**
- Consumes: `VUMeter`, `renderVUCursor` (Task 1); `SetCursor` (Task 2).
- Produces:
  - `type StartFunc func() (*record.Recorder, *transcribe.Streamer, string, error)` — the string is a stream-unavailable note (`""` when streaming started).
  - `func NewClipsModel(startFunc StartFunc, rec *record.Recorder, streamer *transcribe.Streamer, opts record.RecordOpts, clipNumber int, savedMessage string) *Model` (streamer param added).
  - `func (m *Model) SetStreamNote(note string)` — dim note rendered below the transcript.
  - `NewModel`/`NewModelWithStreamer` signatures unchanged. `ShouldTranscribe`/`ClipDone`/`Recorder`/`StreamErr` unchanged.
  - Layout (top→bottom): header (status + duration + clip# left, `NkHz mono/stereo` + dB right-aligned) / mic line / out line / [saved line] / separator / transcript viewport / [stream error or note line] / separator / keys. No waveform, no blank spacer lines.

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/model_test.go`:

```go
package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegoldin/audiomemo/internal/record"
)

func testOpts() record.RecordOpts {
	return record.RecordOpts{
		Device:     "default",
		SampleRate: 48000,
		Channels:   2,
		OutputPath: "/tmp/memo.ogg",
	}
}

// advance sends a WindowSizeMsg and one tick so the model has sized its
// viewport and pushed a level into the VU meter.
func advance(m *Model) *Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(*Model)
	next, _ = m.Update(tickMsg(time.Now()))
	return next.(*Model)
}

func TestModelViewShowsVUCursor(t *testing.T) {
	m := advance(NewModel(nil, testOpts()))
	view := m.View()
	// Level 0 while recording → green ▁ floor cursor.
	if !strings.Contains(view, waveGreen.Render("▁")) {
		t.Errorf("expected view to contain the VU cursor, got:\n%s", view)
	}
}

func TestModelViewHasNoWaveformGrid(t *testing.T) {
	m := advance(NewModel(nil, testOpts()))
	view := m.View()
	// The old animation drew scrolling tick marks; the new layout must not.
	if strings.Contains(view, "┊") {
		t.Errorf("expected no waveform tick marks in view, got:\n%s", view)
	}
}

func TestModelViewShowsStreamNote(t *testing.T) {
	m := NewModel(nil, testOpts())
	m.SetStreamNote("live transcription unavailable: no ElevenLabs API key configured")
	m = advance(m)
	if !strings.Contains(m.View(), "live transcription unavailable") {
		t.Error("expected stream note in view")
	}
}

func TestModelViewHeaderHasDB(t *testing.T) {
	m := advance(NewModel(nil, testOpts()))
	lines := strings.Split(m.View(), "\n")
	if !strings.Contains(lines[0], "dB") {
		t.Errorf("expected dB readout in header line, got: %q", lines[0])
	}
	if !strings.Contains(lines[0], "48kHz stereo") {
		t.Errorf("expected sample-rate info in header line, got: %q", lines[0])
	}
}

func TestModelScrollKeysWithoutStreamer(t *testing.T) {
	m := advance(NewModel(nil, testOpts()))
	// Must not panic and must not quit — scroll keys are always active now.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if next == nil {
		t.Fatal("expected model back from scroll key")
	}
	if next.(*Model).state != StateRecording {
		t.Error("scroll key must not change recording state")
	}
}

func TestModelMutedCursorIsDim(t *testing.T) {
	m := advance(NewModel(nil, testOpts()))
	m.muted = true
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(*Model)
	if !strings.Contains(m.View(), vuCursorMutedStyle.Render("▁")) {
		t.Error("expected dim-gray floor cursor while muted")
	}
}

func TestClipsModelReadyKeysHint(t *testing.T) {
	m := NewClipsModel(nil, nil, nil, testOpts(), 2, "Saved clip 1!")
	m = advance(m)
	view := m.View()
	if !strings.Contains(view, "[space/m] record") {
		t.Errorf("expected READY keys hint, got:\n%s", view)
	}
	if !strings.Contains(view, "Saved clip 1!") {
		t.Errorf("expected saved message, got:\n%s", view)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run TestModel -v`
Expected: FAIL — compile errors (`NewClipsModel` argument count, `SetStreamNote` undefined).

- [ ] **Step 3: Rework `internal/tui/model.go`**

Apply these changes:

**Struct** — replace the `Model` struct with:

```go
type Model struct {
	state        State
	recorder     *record.Recorder
	opts         record.RecordOpts
	startTime    time.Time
	elapsed      time.Duration
	level        float64
	vu           VUMeter
	transcribe   bool // set when user presses Q to quit-and-transcribe
	muted        bool
	clipDone     bool // set when user presses q in clips mode (save clip, continue)
	clipsMode    bool
	clipNumber   int
	savedMessage string // e.g. "Saved clip 3!"
	startFunc    StartFunc
	err          error
	width        int
	height       int
	streamer     *transcribe.Streamer
	transcript   TranscriptViewport
	streamErr    error
	streamNote   string // e.g. "live transcription unavailable: ..."
}
```

(Removed: `tick`, `anim`, `liveTranscription`. Added: `vu`, `streamNote`.)

**StartFunc** — replace the type:

```go
// StartFunc creates and starts a new Recorder for a deferred clip, optionally
// with a live-transcription Streamer. The string is a stream-unavailable note
// ("" when streaming started) rendered dim below the transcript.
type StartFunc func() (*record.Recorder, *transcribe.Streamer, string, error)
```

**Constructors** — replace all three:

```go
func NewModel(rec *record.Recorder, opts record.RecordOpts) *Model {
	return &Model{
		state:      StateRecording,
		recorder:   rec,
		opts:       opts,
		startTime:  time.Now(),
		level:      -60, // silence floor until the first RMS reading arrives
		transcript: NewTranscriptViewport(60, 10),
	}
}

func NewModelWithStreamer(rec *record.Recorder, opts record.RecordOpts, streamer *transcribe.Streamer) *Model {
	m := NewModel(rec, opts)
	m.streamer = streamer
	return m
}

// NewClipsModel creates a Model for clips mode. If rec is nil, starts in StateReady
// and uses startFunc to create the recorder (and streamer) when the user presses space/m.
func NewClipsModel(startFunc StartFunc, rec *record.Recorder, streamer *transcribe.Streamer, opts record.RecordOpts, clipNumber int, savedMessage string) *Model {
	initialState := StateRecording
	if rec == nil {
		initialState = StateReady
	}
	return &Model{
		state:        initialState,
		recorder:     rec,
		streamer:     streamer,
		opts:         opts,
		startTime:    time.Now(),
		level:        -60, // silence floor until the first RMS reading arrives
		transcript:   NewTranscriptViewport(60, 10),
		clipsMode:    true,
		clipNumber:   clipNumber,
		savedMessage: savedMessage,
		startFunc:    startFunc,
	}
}

// SetStreamNote sets a dim informational note shown below the transcript,
// e.g. "live transcription unavailable: no ElevenLabs API key configured".
func (m *Model) SetStreamNote(note string) {
	m.streamNote = note
}
```

**Init** — gate streamer listeners on `m.streamer != nil` (the `liveTranscription` flag is gone):

```go
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tickCmd()}
	if m.state == StateRecording {
		cmds = append(cmds, listenLevel(m.recorder), listenDone(m.recorder))
	}
	if m.streamer != nil {
		cmds = append(cmds, listenCommitted(m.streamer), listenPartial(m.streamer), listenStreamErr(m.streamer))
	}
	if m.state == StateReady {
		return cmds[0] // just tickCmd for ready state
	}
	return tea.Batch(cmds...)
}
```

**Update** — replace the `tea.WindowSizeMsg` and `tickMsg` cases:

```go
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// header + mic + out + 2 separators + keys = 6 fixed rows, plus up to
		// 2 optional rows (saved message, stream error/note).
		viewportHeight := msg.Height - 8
		if viewportHeight < 4 {
			viewportHeight = 4
		}
		m.transcript.SetSize(msg.Width, viewportHeight)
		return m, nil

	case tickMsg:
		if m.state == StateRecording {
			m.elapsed = time.Since(m.startTime)
		}
		if m.state == StateRecording && !m.muted {
			m.vu.Push(dbToLevel(m.level))
		} else {
			m.vu.Push(0)
		}
		paused := m.muted || m.state != StateRecording
		m.transcript.SetCursor(renderVUCursor(m.vu.Level(), paused))
		return m, tickCmd()
```

**handleKey** — remove the `if m.liveTranscription` gate around scroll keys (they are always forwarded now):

```go
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg.String() {
	case "up", "down", "pgup", "pgdown", "end":
		m.transcript, cmd = m.transcript.Update(msg)
		return m, cmd
	}
	// ... rest unchanged except the READY-start branch below ...
```

Replace the READY-start branch inside the `"m", " "` case:

```go
		if m.state == StateReady {
			// Start recording the next clip
			if m.startFunc != nil {
				rec, streamer, note, err := m.startFunc()
				if err != nil {
					m.err = err
					return m, tea.Quit
				}
				m.recorder = rec
				m.streamer = streamer
				m.streamNote = note
			}
			m.state = StateRecording
			m.startTime = time.Now()
			m.elapsed = 0
			m.savedMessage = ""
			m.muted = false
			cmds := []tea.Cmd{listenLevel(m.recorder), listenDone(m.recorder)}
			if m.streamer != nil {
				cmds = append(cmds, listenCommitted(m.streamer), listenPartial(m.streamer), listenStreamErr(m.streamer))
			}
			return m, tea.Batch(cmds...)
		}
```

**View** — replace the whole function:

```go
func (m *Model) View() string {
	// Status line
	var status string
	switch {
	case m.state == StateSaved:
		status = savedStyle.Render("✓ SAVED")
	case m.state == StateReady:
		status = readyStyle.Render("⏳ READY")
	case m.muted:
		status = muteStyle.Render("🔇 MUTED")
	default:
		status = recStyle.Render("● REC")
	}

	dur := formatDuration(m.elapsed)
	var clipInfo string
	if m.clipsMode {
		clipInfo = dimStyle.Render(fmt.Sprintf("  clip %d", m.clipNumber))
	}
	left := fmt.Sprintf("  %s  %s%s", status, dur, clipInfo)
	info := dimStyle.Render(fmt.Sprintf("%dkHz %s", m.opts.SampleRate/1000, channelStr(m.opts.Channels)))
	right := info + vuDBText.Render("  "+formatDB(m.vu.Level())+"  ")
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 2 {
		pad = 2
	}
	header := left + strings.Repeat(" ", pad) + right

	// Info
	micDisplay := m.opts.Device
	if m.opts.DeviceLabel != "" {
		micDisplay = m.opts.DeviceLabel
	}
	micLine := infoStyle.Render(fmt.Sprintf("  mic: %s", micDisplay))
	outLine := infoStyle.Render(fmt.Sprintf("  out: %s", m.opts.OutputPath))

	// Saved message (clips mode, between clips)
	var savedLine string
	if m.savedMessage != "" {
		savedLine = savedStyle.Render(fmt.Sprintf("  ✓ %s", m.savedMessage))
	}

	// Keys
	var keys string
	if m.clipsMode {
		if m.state == StateReady {
			keys = dimStyle.Render("  [space/m] record  [q]uit  [Q]uit+transcribe")
		} else {
			keys = dimStyle.Render("  [↑↓] scroll  [m]ute  [q] save clip  [Q]uit+transcribe")
		}
	} else {
		keys = dimStyle.Render("  [↑↓] scroll  [m]ute  [q]uit  [Q]uit+transcribe")
	}

	sepWidth := m.width
	if sepWidth <= 0 {
		sepWidth = 60
	}
	sep := dimStyle.Render(strings.Repeat("─", sepWidth))

	parts := []string{header, micLine, outLine}
	if savedLine != "" {
		parts = append(parts, savedLine)
	}
	parts = append(parts, sep, m.transcript.View())
	if m.streamErr != nil {
		parts = append(parts, streamErrStyle.Render(fmt.Sprintf("  ⚠ live transcription stopped: %v", m.streamErr)))
	} else if m.streamNote != "" {
		parts = append(parts, dimStyle.Render("  "+m.streamNote))
	}
	parts = append(parts, sep, keys)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
```

The `committedMsg`/`partialMsg`/`streamErrMsg`/`levelMsg`/`doneMsg` cases, the other key handlers, the listen helpers, styles, `formatDuration`, and `channelStr` are unchanged.

- [ ] **Step 4: Shim the call sites in `cmd/record.go` so the build stays green**

In `runClips` (full rework lands in Task 5), update the two `NewClipsModel` calls and `startRec`:

```go
		startRec := func() (*record.Recorder, *transcribe.Streamer, string, error) {
			rec, err := record.Start(opts)
			return rec, nil, "", err
		}

		var model *tui.Model
		if clipNumber == 1 {
			// First clip: start recording immediately
			rec, _, _, err := startRec()
			if err != nil {
				return err
			}
			model = tui.NewClipsModel(nil, rec, nil, opts, clipNumber, "")
		} else {
			// Subsequent clips: show ready state, wait for user to start
			model = tui.NewClipsModel(startRec, nil, nil, opts, clipNumber, savedMessage)
		}
```

Add `"github.com/joegoldin/audiomemo/internal/transcribe"` to imports if the compiler flags it (it is already imported).

- [ ] **Step 5: Run build and tests**

Run: `go build ./... && go test ./internal/tui/ -v`
Expected: build OK, all tui tests PASS (animation tests still exist and pass — `animation.go` is now dead code, removed in Task 6).

- [ ] **Step 6: Commit**

```bash
git add internal/tui/model.go internal/tui/model_test.go cmd/record.go
git commit -m "feat(tui): transcript-first recording screen with VU cursor, drop waveform layout"
```

---

### Task 4: Always-live single recording + transcript promotion

**Files:**
- Modify: `cmd/record.go`
- Test: `cmd/record_test.go` (new file)

**Interfaces:**
- Consumes: `liveTranscriptPathFor(audioPath)` and `transcriptPathFor(audioPath, format)` from `cmd/transcribe.go`; `transcribe.FormatText`; `tui.NewModel(...).SetStreamNote(...)` (Task 3).
- Produces: `func promoteLiveTranscript(audioPath string) (string, error)` — copies `<base>-live.txt` to `<base>.txt`, preserving the live file; returns `("", nil)` when the live file is missing or blank; returns the canonical path when promoted. Task 5 reuses it per clip.

- [ ] **Step 1: Write the failing tests**

Create `cmd/record_test.go`:

```go
package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromoteLiveTranscript(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "memo.ogg")
	live := filepath.Join(dir, "memo-live.txt")
	if err := os.WriteFile(live, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	promoted, err := promoteLiveTranscript(audio)
	if err != nil {
		t.Fatalf("promote failed: %v", err)
	}
	want := filepath.Join(dir, "memo.txt")
	if promoted != want {
		t.Errorf("expected promoted path %q, got %q", want, promoted)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("canonical transcript not written: %v", err)
	}
	if string(data) != "hello world\n" {
		t.Errorf("unexpected content: %q", data)
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("live file should be preserved: %v", err)
	}
}

func TestPromoteLiveTranscriptMissing(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "memo.ogg")

	promoted, err := promoteLiveTranscript(audio)
	if err != nil {
		t.Fatalf("expected nil error for missing live file, got %v", err)
	}
	if promoted != "" {
		t.Errorf("expected no promotion, got %q", promoted)
	}
	if _, err := os.Stat(filepath.Join(dir, "memo.txt")); !os.IsNotExist(err) {
		t.Error("canonical transcript should not exist")
	}
}

func TestPromoteLiveTranscriptEmpty(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "memo.ogg")
	if err := os.WriteFile(filepath.Join(dir, "memo-live.txt"), []byte("  \n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	promoted, err := promoteLiveTranscript(audio)
	if err != nil {
		t.Fatalf("expected nil error for blank live file, got %v", err)
	}
	if promoted != "" {
		t.Errorf("expected skip for blank live file, got %q", promoted)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/ -run TestPromote -v`
Expected: FAIL — compile error `undefined: promoteLiveTranscript`.

- [ ] **Step 3: Implement `promoteLiveTranscript` in `cmd/record.go`**

Append at the end of the file:

```go
// promoteLiveTranscript copies the live transcript (<base>-live.txt) to the
// canonical transcript path (<base>.txt) so a transcript always exists after
// recording, even without a batch run. The live file is preserved; a later
// batch transcription overwrites the canonical file with the diarized result.
// Missing or blank live files are skipped (returns "", nil).
func promoteLiveTranscript(audioPath string) (string, error) {
	livePath := liveTranscriptPathFor(audioPath)
	data, err := os.ReadFile(livePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", nil
	}
	dest := transcriptPathFor(audioPath, transcribe.FormatText)
	if err := os.WriteFile(dest, data, 0644); err != nil {
		return "", err
	}
	return dest, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/ -run TestPromote -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Rewire `runRecord` for always-live + promote**

In `runRecord`, replace the streamer creation block (currently `if rTranscribe && cfg.Transcribe.ElevenLabs.APIKey != ""`):

```go
	var streamer *transcribe.Streamer
	streamNote := ""
	if cfg.Transcribe.ElevenLabs.APIKey != "" {
		streamer = transcribe.NewStreamer(
			cfg.Transcribe.ElevenLabs.APIKey,
			cfg.Transcribe.ElevenLabs.StoreInCloud,
		)
	} else {
		streamNote = "live transcription unavailable: no ElevenLabs API key configured"
	}
```

Replace the streamer-start failure branch so the reason reaches the TUI:

```go
	if streamer != nil {
		transcriptPath := liveTranscriptPathFor(outputPath)
		if err := streamer.Start(context.Background(), rec.PCMReader, transcriptPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: live transcription failed to start: %v\n", err)
			streamNote = fmt.Sprintf("live transcription unavailable: %v", err)
			streamer = nil
			// ffmpeg was started with the PCM pipe output and nothing else will
			// read it. Drain it in the background so ffmpeg doesn't block on
			// pipe writes (which would freeze the primary encoded output too).
			go io.Copy(io.Discard, rec.PCMReader)
		}
	}
```

Replace the model-creation branch:

```go
		if streamer != nil {
			model = tui.NewModelWithStreamer(rec, opts, streamer)
		} else {
			model = tui.NewModel(rec, opts)
			model.SetStreamNote(streamNote)
		}
```

After the existing `if streamer != nil { streamer.Stop() }` block (and before `fmt.Println(outputPath)`), add the promotion:

```go
	// Promote the live transcript to the canonical <base>.txt so a transcript
	// always exists. When batch transcription runs next (Q or -t), it
	// overwrites the canonical file with the higher-quality result.
	if promoted, err := promoteLiveTranscript(outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to promote live transcript: %v\n", err)
	} else if promoted != "" && rVerbose {
		fmt.Fprintf(os.Stderr, "Saved live transcript to %s\n", promoted)
	}
```

Also update the stale comment above `runPostTranscribe`'s call site (`// Always run batch after recording. ...`) to:

```go
	if shouldTranscribe {
		// Batch transcribe overwrites the promoted live transcript at
		// <base>.txt with the diarized full result. The live preview is
		// preserved at <base>-live.txt.
		return runPostTranscribe(outputPath)
	}
```

- [ ] **Step 6: Update help text**

Replace `recordCmd`'s `Long` with:

```go
	Long: `Record audio from your microphone with a live transcript view. The cursor
at the end of the transcript doubles as a VU meter, changing height and color
with the mic level.

Live transcription streams automatically whenever an ElevenLabs API key is
configured. Press q to stop and keep the live transcript at <name>.txt; press
Q to stop and additionally run the higher-quality batch transcription, which
overwrites <name>.txt (the live preview is kept at <name>-live.txt either way).

An optional name can be passed as positional arguments to label the recording.
Multiple words are joined with underscores.

Examples:
  record
  record meeting
  rec standup -t
  record -d 5m --no-tui
  record -D "Built-in Microphone" -t --transcribe-args="--backend deepgram"`,
```

Replace the `-t` flag registration:

```go
	recordCmd.Flags().BoolVarP(&rTranscribe, "transcribe", "t", false, "always run batch transcription on exit (as if quitting with Q)")
```

- [ ] **Step 7: Build and run the full test suite**

Run: `go build ./... && go test ./...`
Expected: PASS — including `TestRecordHelp` (flag names unchanged) and the whisper-cpp integration tests.

- [ ] **Step 8: Commit**

```bash
git add cmd/record.go cmd/record_test.go
git commit -m "feat(record): always-on live transcription with q=live/Q=batch promote semantics"
```

---

### Task 5: Clips mode live transcription

**Files:**
- Modify: `cmd/record.go` (`runClips` and its call site)

**Interfaces:**
- Consumes: `StartFunc` 4-value signature and `NewClipsModel` streamer param (Task 3); `promoteLiveTranscript` (Task 4); `transcribe.NewStreamer`, `Streamer.Start/Stop` (single-use per clip — `Stop` closes its channels).
- Produces: no new exported API. Behavior: each clip streams to `<name>_clipN-live.txt`, promotes to `<name>_clipN.txt` on save; `Q` batch-retranscribes all saved clips.

- [ ] **Step 1: Update the `runClips` call site in `runRecord`**

```go
	if rClips {
		return runClips(cfg, name, format, sampleRate, channels, devices, deviceLabel, outputDir)
	}
```

- [ ] **Step 2: Replace `runClips` entirely**

```go
func runClips(cfg *config.Config, name, format string, sampleRate, channels int, devices []string, deviceLabel, outputDir string) error {
	var savedPaths []string
	clipNumber := 1
	savedMessage := ""
	apiKey := cfg.Transcribe.ElevenLabs.APIKey

	for {
		outputPath := filepath.Join(outputDir, record.GenerateClipFilename(format, name, clipNumber))
		livePath := liveTranscriptPathFor(outputPath)
		opts := record.RecordOpts{
			Device:      devices[0],
			Devices:     devices,
			DeviceLabel: deviceLabel,
			Format:      format,
			SampleRate:  sampleRate,
			Channels:    channels,
			OutputPath:  outputPath,
			LivePCM:     apiKey != "",
		}

		// Streamers are single-use (Stop closes their channels), so each clip
		// gets a fresh one. clipStreamer holds the streamer created by
		// startRec so it can be stopped after the clip's TUI exits.
		var clipStreamer *transcribe.Streamer
		startRec := func() (*record.Recorder, *transcribe.Streamer, string, error) {
			rec, err := record.Start(opts)
			if err != nil {
				return nil, nil, "", err
			}
			if apiKey == "" {
				return rec, nil, "live transcription unavailable: no ElevenLabs API key configured", nil
			}
			s := transcribe.NewStreamer(apiKey, cfg.Transcribe.ElevenLabs.StoreInCloud)
			if err := s.Start(context.Background(), rec.PCMReader, livePath); err != nil {
				// Nothing else reads the PCM pipe; drain it so ffmpeg doesn't
				// block on pipe writes. This clip records without live text;
				// the next clip retries with a fresh streamer.
				go io.Copy(io.Discard, rec.PCMReader)
				return rec, nil, fmt.Sprintf("live transcription unavailable: %v", err), nil
			}
			clipStreamer = s
			return rec, s, "", nil
		}

		var model *tui.Model
		if clipNumber == 1 {
			// First clip: start recording immediately
			rec, streamer, note, err := startRec()
			if err != nil {
				return err
			}
			model = tui.NewClipsModel(nil, rec, streamer, opts, clipNumber, "")
			model.SetStreamNote(note)
		} else {
			// Subsequent clips: show ready state, wait for user to start
			model = tui.NewClipsModel(startRec, nil, nil, opts, clipNumber, savedMessage)
		}

		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return err
		}

		rec := model.Recorder()
		recorded := rec != nil

		if recorded {
			err := rec.Wait()
			if clipStreamer != nil {
				clipStreamer.Stop()
			}
			if err != nil {
				// ffmpeg can exit non-zero even after writing a valid file —
				// notably when the live PCM pipe tears down on 'q'. Treat as a
				// warning; if the file is corrupt the batch step fails loudly.
				fmt.Fprintf(os.Stderr, "Warning: recording exited with error: %v\n", err)
			}
			savedPaths = append(savedPaths, outputPath)
			fmt.Println(outputPath)
			if _, perr := promoteLiveTranscript(outputPath); perr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to promote live transcript: %v\n", perr)
			}
		}

		if model.ShouldTranscribe() {
			for _, path := range savedPaths {
				if err := runPostTranscribe(path); err != nil {
					fmt.Fprintf(os.Stderr, "transcribe %s: %v\n", path, err)
				}
			}
			return nil
		}

		if model.ClipDone() {
			savedMessage = fmt.Sprintf("Saved clip %d!", clipNumber)
			clipNumber++
			continue
		}

		// ctrl+c or q from ready state — done
		return nil
	}
}
```

Note the deliberate behavior change: a non-zero `rec.Wait()` now warns instead of discarding the clip, matching the single-recording path (the PCM-pipe teardown can surface broken-pipe as non-zero exit even when the file is valid).

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./...`
Expected: PASS. Also verify: `go vet ./cmd/ ./internal/...`
Expected: no findings.

- [ ] **Step 4: Commit**

```bash
git add cmd/record.go
git commit -m "feat(clips): per-clip live transcription with promote-on-save"
```

---

### Task 6: Remove the waveform, update docs, final verification

**Files:**
- Delete: `internal/tui/animation.go`, `internal/tui/animation_test.go`
- Modify: `internal/tui/vu.go` (absorb `heightBlocks` + wave color styles)
- Modify: `README.md`

**Interfaces:**
- Consumes: nothing new.
- Produces: `heightBlocks`, `waveGreen`, `waveYellow`, `waveRed` now live in `internal/tui/vu.go` (same names, same package — no caller changes). `waveTick`/`waveTickHi` and the `Animation` type are gone.

- [ ] **Step 1: Move the shared constants into `internal/tui/vu.go`**

Add near the top of `vu.go` (below the existing `vuDBText`/`vuCursorMutedStyle` vars):

```go
// Fractional block characters for the VU cursor, growing from the bottom of
// the cell. Index 0 = empty (unused by the cursor), 8 = full block.
var heightBlocks = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

var (
	waveGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	waveYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308"))
	waveRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
)
```

- [ ] **Step 2: Delete the animation files**

```bash
git rm internal/tui/animation.go internal/tui/animation_test.go
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./internal/tui/ -v`
Expected: build OK, PASS. If the compiler reports duplicate definitions, the constants were added before deleting — the delete resolves it.

- [ ] **Step 4: Update README.md**

Line 35, replace:

```
Record audio with a live TUI showing a scrolling waveform and VU meter.
```

with:

```
Record audio with a live TUI showing a streaming transcript. The cursor at
the end of the transcript doubles as a VU meter (height and color track the
mic level). Live transcription is always on when an ElevenLabs key is set.
```

Lines 45–46 (`-t` flag), replace:

```
    -t, --transcribe             transcribe after recording (live streaming
                                 when ElevenLabs is configured)
```

with:

```
    -t, --transcribe             always run batch transcription on exit
                                 (as if quitting with Q)
```

Line 56–58 keybindings, replace:

```
    q           stop and save
    Q           stop, save, and transcribe
    ↑/↓         scroll transcript (when live transcribing)
```

with:

```
    q           stop, save, and keep the live transcript
    Q           stop, save, and batch-retranscribe (higher quality)
    ↑/↓         scroll transcript
```

Replace the LIVE TRANSCRIPTION section body (lines 170–181) with:

```
Whenever an ElevenLabs API key is configured, audio is streamed in realtime
to ElevenLabs for live speech-to-text — no flag needed. The transcript is the
main content of the recording TUI; the cursor at the insertion point doubles
as a VU meter.

- Text appears as you talk (partial results in gray, committed text in white)
- Auto-scrolls to show latest text; scroll up to browse history
- `↓ live` indicator appears when scrolled up
- Live transcript is saved incrementally to <name>-live.txt (crash-safe)
- On quit, the live transcript is promoted to <name>.txt; quitting with Q (or
  passing -t) then overwrites it with the batch result
- If no ElevenLabs key is configured, recording shows a lone VU cursor and
  transcripts are only produced by Q / -t batch runs
```

- [ ] **Step 5: Final verification gates**

```bash
go build ./...
go vet ./cmd/ ./internal/...
go test ./...
gofmt -l cmd internal
grep -rn "Animation\|waveform" internal/ cmd/ --include="*.go"
```

Expected: build/vet/test clean; `gofmt -l` prints nothing; the grep returns no remaining references (comments included).

- [ ] **Step 6: Manual smoke test (requires a mic + optionally an ElevenLabs key)**

```bash
go run . record --temp -d 5s smoke
```

Expected: transcript-first screen, VU cursor pulsing at the insertion point (or lone cursor + dim unavailable note without a key); after exit, with a key configured, `<tmp>/...smoke.txt` exists (promoted live transcript) alongside `...smoke-live.txt`.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(tui)!: remove waveform animation; transcript-first UI everywhere"
```
