# Clips Mode & Mute Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add clips recording mode (`--clips`/`-C`) for sequential multi-clip sessions, replace pause with per-stream PulseAudio mute, and allow multi-word positional args joined with `_`.

**Architecture:** Three independent changes that compose: (1) multi-word name joining in CLI, (2) mute replacing pause in recorder + TUI, (3) clips loop in `runRecord` with new TUI states. Each builds on the prior.

**Tech Stack:** Go, Cobra CLI, Bubbletea TUI, ffmpeg, PulseAudio (`pactl`)

---

### Task 1: Multi-word positional args joined with underscore

**Files:**
- Modify: `cmd/record.go:33-48` (cobra command definition, Args)
- Modify: `cmd/record.go:169-172` (name from positional args)
- Modify: `integration_test.go` (add test)

**Step 1: Write the failing test**

Add to `integration_test.go`:

```go
func TestRecordMultiWordName(t *testing.T) {
	stdout, _, err := run(t, "record", "--help")
	if err != nil {
		t.Fatalf("record --help failed: %v", err)
	}
	// Should accept arbitrary args now, not just [name]
	if !strings.Contains(stdout, "[name") {
		t.Error("help should show name argument")
	}
}
```

Also add a unit test in a new file `internal/record/recorder_test.go`:

```go
package record

import "testing"

func TestGenerateFilenameWithLabel(t *testing.T) {
	name := GenerateFilename("ogg", "my_cool_meeting")
	if !strings.HasPrefix(name, "my_cool_meeting-") {
		t.Errorf("expected prefix my_cool_meeting-, got %s", name)
	}
	if !strings.HasSuffix(name, ".ogg") {
		t.Errorf("expected .ogg suffix, got %s", name)
	}
}
```

**Step 2: Run tests to verify they pass (baseline)**

Run: `go test ./internal/record/ -run TestGenerateFilename -v`
Run: `go test . -run TestRecordMultiWordName -v`

**Step 3: Implement multi-word name joining**

In `cmd/record.go`, change the command definition:

```go
var recordCmd = &cobra.Command{
	Use:     "record [flags] [name ...]",
	Aliases: []string{"rec"},
	Short:   "Record audio from microphone",
	Long: `Record audio from your microphone with a live TUI showing VU meter and animation.

An optional name can be passed as positional arguments to label the recording.
Multiple words are joined with underscores.

Examples:
  record
  record meeting
  record my cool meeting
  rec standup -t
  record -d 5m --no-tui
  record -D "Built-in Microphone" -t --transcribe-args="--backend deepgram"`,
	Args: cobra.ArbitraryArgs,
	RunE: runRecord,
}
```

In `runRecord`, replace the name resolution block (lines 168-172):

```go
	// Positional args joined with _ take priority, then -n flag.
	name := rName
	if len(args) > 0 {
		name = strings.Join(args, "_")
	}
```

**Step 4: Run tests**

Run: `go test ./... -count=1 -v 2>&1 | head -100`

**Step 5: Commit**

```bash
git add cmd/record.go internal/record/recorder_test.go
git commit -m "feat: join multi-word positional args with underscore for recording name"
```

---

### Task 2: Add mute to Recorder (replace pause)

**Files:**
- Modify: `internal/record/recorder.go` (replace Pause with mute methods)
- Create: `internal/record/mute.go` (pactl mute logic)

**Step 1: Create mute implementation**

Create `internal/record/mute.go`:

```go
package record

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// findSourceOutputByPID finds the PulseAudio source-output ID for a given PID.
// Returns -1 if not found.
func findSourceOutputByPID(pid int) (int, error) {
	// pactl list source-outputs short outputs lines like:
	// 42	alsa_input.pci-0000_00_1f.3.analog-stereo	12345	...
	// We need to find the one matching our ffmpeg PID.

	// Use pactl list source-outputs to get detailed info including application.process.id
	out, err := exec.Command("pactl", "list", "source-outputs").CombinedOutput()
	if err != nil {
		return -1, fmt.Errorf("pactl list source-outputs: %w", err)
	}

	pidStr := strconv.Itoa(pid)
	lines := strings.Split(string(out), "\n")
	currentIndex := -1
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Source Output #") {
			idx, err := strconv.Atoi(strings.TrimPrefix(line, "Source Output #"))
			if err == nil {
				currentIndex = idx
			}
		}
		if strings.Contains(line, "application.process.id") && strings.Contains(line, `"`+pidStr+`"`) {
			return currentIndex, nil
		}
	}
	return -1, fmt.Errorf("no source-output found for PID %d", pid)
}

// muteSourceOutput sets or unsets mute on a PulseAudio source-output.
func muteSourceOutput(sourceOutputID int, mute bool) error {
	val := "0"
	if mute {
		val = "1"
	}
	return exec.Command("pactl", "set-source-output-mute",
		strconv.Itoa(sourceOutputID), val).Run()
}
```

**Step 2: Add mute fields and methods to Recorder**

In `internal/record/recorder.go`, add fields to Recorder struct:

```go
type Recorder struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stderr         io.ReadCloser
	Level          chan float64
	Done           chan error
	done           chan struct{}
	exitErr        error
	muted          bool
	sourceOutputID int // PulseAudio source-output ID for per-stream mute
}
```

Remove the `paused` field and the `Pause()` method entirely.

Add mute methods:

```go
// ToggleMute toggles per-stream mute on the ffmpeg PulseAudio source-output.
func (r *Recorder) ToggleMute() {
	r.muted = !r.muted
	if r.sourceOutputID >= 0 {
		muteSourceOutput(r.sourceOutputID, r.muted)
	}
}

// IsMuted returns whether the recorder is currently muted.
func (r *Recorder) IsMuted() bool {
	return r.muted
}

// discoverSourceOutput finds the PulseAudio source-output for this recorder's
// ffmpeg process. Called in a goroutine after Start.
func (r *Recorder) discoverSourceOutput() {
	if r.cmd.Process == nil {
		return
	}
	pid := r.cmd.Process.Pid
	// Retry briefly — PulseAudio may take a moment to register the stream.
	for i := 0; i < 10; i++ {
		id, err := findSourceOutputByPID(pid)
		if err == nil {
			r.sourceOutputID = id
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
```

In the `Start` function, initialize `sourceOutputID` to -1 and launch discovery:

```go
	r := &Recorder{
		cmd:            cmd,
		stdin:          stdin,
		stderr:         stderr,
		Level:          make(chan float64, 10),
		Done:           make(chan error, 1),
		done:           make(chan struct{}),
		sourceOutputID: -1,
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	go r.discoverSourceOutput()
	go r.parseStderr()
```

Update `Stop()` to remove pause references:

```go
func (r *Recorder) Stop() {
	// Unmute before stopping so ffmpeg captures final audio cleanly.
	if r.muted && r.sourceOutputID >= 0 {
		muteSourceOutput(r.sourceOutputID, false)
		r.muted = false
	}
	r.stdin.Write([]byte("q"))
	r.stdin.Close()
}
```

**Step 3: Run tests**

Run: `go build ./...`
Run: `go test ./... -count=1 -v 2>&1 | head -100`

**Step 4: Commit**

```bash
git add internal/record/recorder.go internal/record/mute.go
git commit -m "feat: replace pause with per-stream PulseAudio mute"
```

---

### Task 3: Update TUI to use mute instead of pause

**Files:**
- Modify: `internal/tui/model.go` (replace pause state/keys with mute)

**Step 1: Update Model struct and state machine**

Remove `StatePaused` from the State enum. Remove `pauseStart` and `pauseTotal` fields. Add `muted bool` field.

```go
type State int

const (
	StateRecording State = iota
	StateSaved
)

type Model struct {
	state      State
	recorder   *record.Recorder
	opts       record.RecordOpts
	startTime  time.Time
	elapsed    time.Duration
	level      float64
	tick       int
	anim       *Animation
	transcribe bool
	muted      bool
	err        error
	width      int
	height     int
}
```

**Step 2: Update key handling**

Replace the `p`/`space` pause handler with `m` mute handler:

```go
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))):
		m.recorder.Stop()
		m.state = StateSaved
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("q"))):
		m.recorder.Stop()
		m.state = StateSaved
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("Q"))):
		m.recorder.Stop()
		m.state = StateSaved
		m.transcribe = true
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("m"))):
		if m.state == StateRecording {
			m.recorder.ToggleMute()
			m.muted = m.recorder.IsMuted()
		}
		return m, nil
	}
	return m, nil
}
```

**Step 3: Update View**

Update status line rendering and key help:

```go
func (m *Model) View() string {
	var status string
	switch {
	case m.state == StateSaved:
		status = savedStyle.Render("✓ SAVED")
	case m.muted:
		status = pauseStyle.Render("🔇 MUTED")
	default:
		status = recStyle.Render("● REC")
	}

	dur := formatDuration(m.elapsed)
	info := fmt.Sprintf("%dkHz %s", m.opts.SampleRate/1000, channelStr(m.opts.Channels))
	header := fmt.Sprintf("  %s  %s       %s", status, dur, dimStyle.Render(info))

	paused := m.muted
	animLevel := dbToLevel(m.level)
	animView := m.anim.Render(m.tick, animLevel, paused)

	dbStr := vuDBText.Render(" " + formatDB(m.anim.SmoothedLevel()))
	center := lipgloss.JoinHorizontal(lipgloss.Center, animView, dbStr)

	micDisplay := m.opts.Device
	if m.opts.DeviceLabel != "" {
		micDisplay = m.opts.DeviceLabel
	}
	micLine := infoStyle.Render(fmt.Sprintf("  mic: %s", micDisplay))
	outLine := infoStyle.Render(fmt.Sprintf("  out: %s", m.opts.OutputPath))

	keys := dimStyle.Render("  [m]ute  [q]uit  [Q]uit+transcribe")

	return lipgloss.JoinVertical(lipgloss.Left,
		header, "", center, "", micLine, outLine, "", keys,
	)
}
```

**Step 4: Update tick handler**

Remove pause time tracking — elapsed always counts during recording state:

```go
	case tickMsg:
		if m.state == StateRecording {
			m.elapsed = time.Since(m.startTime)
			m.tick++
		}
		return m, tickCmd()
```

**Step 5: Run tests and build**

Run: `go build ./...`
Run: `go test ./... -count=1 -v 2>&1 | head -100`

**Step 6: Commit**

```bash
git add internal/tui/model.go
git commit -m "feat: replace pause UI with mute toggle in TUI"
```

---

### Task 4: Add `--clips`/`-C` flag and clips filename generation

**Files:**
- Modify: `cmd/record.go` (add flag, update GenerateFilename call)
- Modify: `internal/record/recorder.go` (add GenerateClipFilename)
- Modify: `internal/record/recorder_test.go` (add test)

**Step 1: Add GenerateClipFilename**

In `internal/record/recorder.go`:

```go
// GenerateClipFilename generates a filename for a clip with a sequence number.
// Format: {label}-{NNN}-{timestamp}.{format}
func GenerateClipFilename(format, label string, clipNumber int) string {
	ts := time.Now().Format("2006-01-02T15-04-05")
	return fmt.Sprintf("%s-%03d-%s.%s", label, clipNumber, ts, format)
}
```

**Step 2: Add test**

In `internal/record/recorder_test.go`:

```go
func TestGenerateClipFilename(t *testing.T) {
	name := GenerateClipFilename("ogg", "interview", 3)
	if !strings.HasPrefix(name, "interview-003-") {
		t.Errorf("expected prefix interview-003-, got %s", name)
	}
	if !strings.HasSuffix(name, ".ogg") {
		t.Errorf("expected .ogg suffix, got %s", name)
	}
}
```

**Step 3: Add `--clips`/`-C` flag**

In `cmd/record.go`, add the flag variable and registration:

```go
var (
	// ... existing vars ...
	rClips bool
)

func init() {
	// ... existing flags ...
	recordCmd.Flags().BoolVarP(&rClips, "clips", "C", false, "clips mode: record multiple clips sequentially")
}
```

**Step 4: Run tests**

Run: `go test ./internal/record/ -run TestGenerateClipFilename -v`
Run: `go build ./...`

**Step 5: Commit**

```bash
git add cmd/record.go internal/record/recorder.go internal/record/recorder_test.go
git commit -m "feat: add --clips/-C flag and clip filename generation"
```

---

### Task 5: Add StateReady and clips mode to TUI Model

**Files:**
- Modify: `internal/tui/model.go` (add StateReady, clips fields, updated key handling and view)

**Step 1: Add new state and fields**

```go
type State int

const (
	StateRecording State = iota
	StateReady
	StateSaved
)

type Model struct {
	state        State
	recorder     *record.Recorder
	opts         record.RecordOpts
	startTime    time.Time
	elapsed      time.Duration
	level        float64
	tick         int
	anim         *Animation
	transcribe   bool
	muted        bool
	clipDone     bool   // true when user pressed q in clips mode (save clip, continue)
	clipsMode    bool
	clipNumber   int
	savedMessage string // e.g. "Saved clip 3!"
	err          error
	width        int
	height       int
}
```

**Step 2: Add constructor for clips mode**

```go
func NewClipsModel(rec *record.Recorder, opts record.RecordOpts, clipNumber int, savedMessage string) *Model {
	initialState := StateRecording
	if savedMessage != "" {
		initialState = StateReady
	}
	return &Model{
		state:        initialState,
		recorder:     rec,
		opts:         opts,
		startTime:    time.Now(),
		anim:         NewAnimation(60, 9),
		clipsMode:    true,
		clipNumber:   clipNumber,
		savedMessage: savedMessage,
	}
}

// ClipDone returns true if the user pressed q in clips mode to save and continue.
func (m *Model) ClipDone() bool {
	return m.clipDone
}
```

**Step 3: Update Init to handle StateReady**

```go
func (m *Model) Init() tea.Cmd {
	if m.state == StateReady {
		return tickCmd() // tick for UI updates, but no recording yet
	}
	return tea.Batch(tickCmd(), listenLevel(m.recorder), listenDone(m.recorder))
}
```

**Step 4: Update key handling for clips mode**

```go
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))):
		if m.state == StateReady {
			return m, tea.Quit
		}
		m.recorder.Stop()
		m.state = StateSaved
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("q"))):
		if m.state == StateReady {
			// In ready state, q just quits
			return m, tea.Quit
		}
		m.recorder.Stop()
		m.state = StateSaved
		if m.clipsMode {
			m.clipDone = true
		}
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("Q"))):
		if m.state == StateReady {
			m.transcribe = true
			return m, tea.Quit
		}
		m.recorder.Stop()
		m.state = StateSaved
		m.transcribe = true
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("m", " "))):
		if m.state == StateReady {
			// Start recording
			m.state = StateRecording
			m.startTime = time.Now()
			m.savedMessage = ""
			return m, tea.Batch(listenLevel(m.recorder), listenDone(m.recorder))
		}
		if m.state == StateRecording {
			m.recorder.ToggleMute()
			m.muted = m.recorder.IsMuted()
		}
		return m, nil
	}
	return m, nil
}
```

Note: in regular (non-clips) mode, `space` now also toggles mute (same as `m`). This is fine since pause is removed.

**Step 5: Update View for clips mode**

```go
func (m *Model) View() string {
	var status string
	switch {
	case m.state == StateSaved:
		status = savedStyle.Render("✓ SAVED")
	case m.state == StateReady:
		status = pauseStyle.Render("⏳ READY")
	case m.muted:
		status = pauseStyle.Render("🔇 MUTED")
	default:
		status = recStyle.Render("● REC")
	}

	dur := formatDuration(m.elapsed)
	info := fmt.Sprintf("%dkHz %s", m.opts.SampleRate/1000, channelStr(m.opts.Channels))

	var clipInfo string
	if m.clipsMode {
		clipInfo = dimStyle.Render(fmt.Sprintf("  clip %d", m.clipNumber))
	}
	header := fmt.Sprintf("  %s  %s%s       %s", status, dur, clipInfo, dimStyle.Render(info))

	paused := m.muted || m.state == StateReady
	animLevel := dbToLevel(m.level)
	animView := m.anim.Render(m.tick, animLevel, paused)

	dbStr := vuDBText.Render(" " + formatDB(m.anim.SmoothedLevel()))
	center := lipgloss.JoinHorizontal(lipgloss.Center, animView, dbStr)

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
			keys = dimStyle.Render("  [m]ute  [q] save clip  [Q]uit+transcribe")
		}
	} else {
		keys = dimStyle.Render("  [m]ute  [q]uit  [Q]uit+transcribe")
	}

	parts := []string{header, "", center, ""}
	if savedLine != "" {
		parts = append(parts, savedLine)
	}
	parts = append(parts, micLine, outLine, "", keys)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
```

**Step 6: Build and test**

Run: `go build ./...`
Run: `go test ./... -count=1 -v 2>&1 | head -100`

**Step 7: Commit**

```bash
git add internal/tui/model.go
git commit -m "feat: add clips mode states and key handling to TUI"
```

---

### Task 6: Implement clips recording loop in runRecord

**Files:**
- Modify: `cmd/record.go` (clips loop in runRecord)

**Step 1: Implement the clips loop**

In `runRecord`, after the existing device resolution and output dir setup, replace the single-recording block with:

```go
	// Positional args joined with _ take priority, then -n flag.
	name := rName
	if len(args) > 0 {
		name = strings.Join(args, "_")
	}

	if rClips {
		if name == "" {
			return fmt.Errorf("clips mode requires a name: record --clips <name>")
		}
		return runClips(name, format, sampleRate, channels, devices, deviceLabel, outputDir)
	}

	// ... existing single-recording code unchanged ...
```

Add the `runClips` function:

```go
func runClips(name, format string, sampleRate, channels int, devices []string, deviceLabel, outputDir string) error {
	var savedPaths []string
	clipNumber := 1
	savedMessage := ""

	for {
		outputPath := filepath.Join(outputDir, record.GenerateClipFilename(format, name, clipNumber))
		opts := record.RecordOpts{
			Device:      devices[0],
			Devices:     devices,
			DeviceLabel: deviceLabel,
			Format:      format,
			SampleRate:  sampleRate,
			Channels:    channels,
			OutputPath:  outputPath,
		}

		var model *tui.Model
		if clipNumber == 1 {
			// First clip: start recording immediately
			rec, err := record.Start(opts)
			if err != nil {
				return err
			}
			model = tui.NewClipsModel(rec, opts, clipNumber, "")
		} else {
			// Subsequent clips: show ready state, wait for user to start
			// We need to start the recorder when the user presses space/m,
			// so we pass a nil recorder and start it from the TUI.
			// Actually, we start the recorder here but in a ready state.
			rec, err := record.Start(opts)
			if err != nil {
				return err
			}
			model = tui.NewClipsModel(rec, opts, clipNumber, savedMessage)
		}

		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return err
		}

		// Wait for ffmpeg to finish if we were recording
		if model.ClipDone() || model.ShouldTranscribe() {
			if err := model.WaitRecorder(); err != nil {
				return fmt.Errorf("recording failed: %w", err)
			}
			savedPaths = append(savedPaths, outputPath)
			fmt.Println(outputPath)
			savedMessage = fmt.Sprintf("Saved clip %d!", clipNumber)
			clipNumber++
		}

		if model.ShouldTranscribe() {
			// Transcribe all clips
			for _, p := range savedPaths {
				if err := runPostTranscribe(p); err != nil {
					fmt.Fprintf(os.Stderr, "transcribe %s: %v\n", p, err)
				}
			}
			return nil
		}

		if !model.ClipDone() {
			// User pressed ctrl+c or q in ready state — quit
			// If there's a current recording, wait for it
			if model.WasRecording() {
				if err := model.WaitRecorder(); err != nil {
					fmt.Fprintf(os.Stderr, "recording failed: %v\n", err)
				}
				savedPaths = append(savedPaths, outputPath)
				fmt.Println(outputPath)
			}
			return nil
		}
	}
}
```

**Step 2: Add helper methods to Model**

In `internal/tui/model.go`, add:

```go
// WaitRecorder waits for the underlying recorder to finish.
func (m *Model) WaitRecorder() error {
	return m.recorder.Wait()
}

// WasRecording returns true if recording was started (not just ready state).
func (m *Model) WasRecording() bool {
	return m.state == StateSaved
}

// Recorder returns the underlying recorder.
func (m *Model) Recorder() *record.Recorder {
	return m.recorder
}
```

**Step 3: Handle the ready-state recorder problem**

The ready state needs a recorder that isn't started yet. But `record.Start` immediately spawns ffmpeg. We need to defer starting the recorder.

Revise approach: In clips mode after clip 1, don't start the recorder until the user presses space/m. The TUI needs to accept a `startFunc` callback instead.

Update `Model` to support deferred start:

```go
type Model struct {
	// ... existing fields ...
	startFunc func() (*record.Recorder, error) // for deferred start in clips mode
}

func NewClipsModel(startFunc func() (*record.Recorder, error), rec *record.Recorder, opts record.RecordOpts, clipNumber int, savedMessage string) *Model {
	initialState := StateRecording
	if rec == nil {
		initialState = StateReady
	}
	return &Model{
		state:        initialState,
		recorder:     rec,
		opts:         opts,
		startTime:    time.Now(),
		anim:         NewAnimation(60, 9),
		clipsMode:    true,
		clipNumber:   clipNumber,
		savedMessage: savedMessage,
		startFunc:    startFunc,
	}
}
```

Update the `m`/`space` key handler for `StateReady`:

```go
	case key.Matches(msg, key.NewBinding(key.WithKeys("m", " "))):
		if m.state == StateReady {
			if m.startFunc != nil {
				rec, err := m.startFunc()
				if err != nil {
					m.err = err
					return m, tea.Quit
				}
				m.recorder = rec
			}
			m.state = StateRecording
			m.startTime = time.Now()
			m.savedMessage = ""
			return m, tea.Batch(listenLevel(m.recorder), listenDone(m.recorder))
		}
		// ...
```

Update `runClips`:

```go
func runClips(name, format string, sampleRate, channels int, devices []string, deviceLabel, outputDir string) error {
	var savedPaths []string
	clipNumber := 1
	savedMessage := ""

	for {
		outputPath := filepath.Join(outputDir, record.GenerateClipFilename(format, name, clipNumber))
		opts := record.RecordOpts{
			Device:      devices[0],
			Devices:     devices,
			DeviceLabel: deviceLabel,
			Format:      format,
			SampleRate:  sampleRate,
			Channels:    channels,
			OutputPath:  outputPath,
		}

		startRec := func() (*record.Recorder, error) {
			return record.Start(opts)
		}

		var model *tui.Model
		if clipNumber == 1 {
			rec, err := startRec()
			if err != nil {
				return err
			}
			model = tui.NewClipsModel(nil, rec, opts, clipNumber, "")
		} else {
			model = tui.NewClipsModel(startRec, nil, opts, clipNumber, savedMessage)
		}

		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return err
		}

		rec := model.Recorder()
		recorded := rec != nil

		if recorded {
			if err := rec.Wait(); err != nil {
				fmt.Fprintf(os.Stderr, "recording failed: %v\n", err)
			} else {
				savedPaths = append(savedPaths, outputPath)
				fmt.Println(outputPath)
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

**Step 4: Build and test**

Run: `go build ./...`
Run: `go test ./... -count=1 -v 2>&1 | head -100`

**Step 5: Commit**

```bash
git add cmd/record.go internal/tui/model.go
git commit -m "feat: implement clips recording loop with sequential clip saving"
```

---

### Task 7: Integration test for clips flag

**Files:**
- Modify: `integration_test.go`

**Step 1: Add test for clips flag in help**

```go
func TestRecordClipsFlag(t *testing.T) {
	stdout, _, err := run(t, "record", "--help")
	if err != nil {
		t.Fatalf("record --help failed: %v", err)
	}
	if !strings.Contains(stdout, "--clips") {
		t.Error("help should mention --clips flag")
	}
	if !strings.Contains(stdout, "-C") {
		t.Error("help should mention -C shorthand")
	}
}

func TestRecordClipsRequiresName(t *testing.T) {
	_, stderr, err := run(t, "record", "--clips", "--no-tui", "-D", "default")
	if err == nil {
		t.Error("clips mode without name should fail")
	}
	if !strings.Contains(stderr, "requires a name") {
		t.Errorf("error should mention name requirement, got: %s", stderr)
	}
}
```

**Step 2: Run tests**

Run: `go test . -run TestRecordClips -v`

**Step 3: Commit**

```bash
git add integration_test.go
git commit -m "test: add integration tests for clips mode flag"
```

---

### Task 8: Final cleanup and verification

**Step 1: Full build**

Run: `go build ./...`

**Step 2: Full test suite**

Run: `go test ./... -count=1 -v`

**Step 3: Manual smoke test**

Run: `go run . record --help` — verify multi-word args and --clips/-C shown
Run: `go run . record test one two three --no-tui -d 1s -D default` — verify filename is `test_one_two_three-{ts}.ogg`

**Step 4: Commit any remaining fixes**

```bash
git add -A
git commit -m "chore: final cleanup for clips mode and mute"
```
