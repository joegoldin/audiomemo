# Realtime Streaming Transcription Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream audio to ElevenLabs during recording for live transcription displayed in a scrollable TUI viewport, with incremental file saving.

**Architecture:** FFmpeg produces a second PCM output via os.Pipe. A `Streamer` goroutine reads PCM chunks, sends them over a WebSocket to ElevenLabs realtime API, and delivers committed/partial transcript text via channels to the bubbletea TUI. The TUI renders a `bubbles/viewport` below the waveform. Transcript is written to disk incrementally on each commit.

**Tech Stack:** Go, gorilla/websocket, charmbracelet/bubbletea v1, charmbracelet/bubbles/viewport, ElevenLabs realtime STT WebSocket API

---

## File Map

| File | Responsibility | Change Type |
|------|---------------|-------------|
| `internal/transcribe/stream.go` | WebSocket client, PCM chunking, transcript channels, incremental file writing | **New** |
| `internal/transcribe/stream_test.go` | Mock WebSocket server tests for Streamer | **New** |
| `internal/record/recorder.go` | Add PCM pipe output to ffmpeg args, expose `PCMReader` | Modify |
| `internal/record/recorder_test.go` | Test PCM pipe arg generation | Modify |
| `internal/tui/model.go` | Add viewport, streamer integration, scroll behavior, layout changes | Modify |
| `internal/tui/transcript.go` | Transcript viewport widget (word wrap, auto-scroll, `↓ live` indicator) | **New** |
| `internal/tui/transcript_test.go` | Test auto-scroll, word wrap, content building | **New** |
| `cmd/record.go` | Wire up streamer when `-t` + ElevenLabs available, skip batch fallback | Modify |
| `go.mod` / `go.sum` | Add `gorilla/websocket` dependency | Modify |

---

## Phase 1: Dependencies and PCM Pipe

### Task 1.1: Add gorilla/websocket dependency

- [ ] Run `go get github.com/gorilla/websocket` and `go mod vendor`
- [ ] Verify build: `go build ./...`
- [ ] Commit: "chore: add gorilla/websocket dependency"

### Task 1.2: Add PCM pipe output to recorder

The recorder needs to optionally produce a second ffmpeg output: raw PCM at 16kHz mono via an `os.Pipe()`.

- [ ] In `internal/record/recorder.go`, add a `LivePCM bool` field to `RecordOpts`
- [ ] In `internal/record/recorder.go`, add a `PCMReader io.ReadCloser` field to `Recorder` struct
- [ ] Write test in `internal/record/recorder_test.go`: `TestBuildFFmpegArgsWithPCMPipe` — when `LivePCM` is true, the generated args should contain `-f s16le -ar 16000 -ac 1 pipe:3` (or the pipe fd) as a second output. When false, args should be unchanged from current behavior.
- [ ] Run test, confirm it fails
- [ ] Implement: modify `BuildFFmpegArgs` (and `BuildFFmpegArgsMulti`) — when `opts.LivePCM` is true, append additional output args: `-f s16le -ar 16000 -ac 1 pipe:<fd>`. The actual pipe fd is determined at `Start` time, not arg-build time. So instead, add a helper `appendPCMPipeArgs(args []string, pipeFd int) []string` that appends the PCM output args with the correct fd number.
- [ ] In `Start()`: when `opts.LivePCM` is true, create `os.Pipe()`, get the write-end fd, pass it as `cmd.ExtraFiles`, compute the fd number (3 + index in ExtraFiles), call `appendPCMPipeArgs`, set `r.PCMReader` to the read end. Close the write end after `cmd.Start()` (ffmpeg inherits it).
- [ ] Run test, confirm it passes
- [ ] Run full test suite: `go test ./...`
- [ ] Commit: "feat: add PCM pipe output option to recorder for live streaming"

### Task 1.3: Add bubbles/viewport to vendor

- [ ] Add `import _ "github.com/charmbracelet/bubbles/viewport"` temporarily or run `go mod vendor` after adding a real import
- [ ] Verify: `go build ./...`
- [ ] Commit: "chore: vendor bubbles/viewport"

---

## Phase 2: WebSocket Streamer

### Task 2.1: Write Streamer tests

Create `internal/transcribe/stream_test.go` with a mock WebSocket server. Tests:

- [ ] `TestStreamerSessionStarted` — connect to mock server, verify client receives `session_started` and doesn't error
- [ ] `TestStreamerSendsAudioChunks` — feed PCM bytes into the streamer's reader, verify mock server receives `input_audio_chunk` messages with base64 audio data and correct `sample_rate`
- [ ] `TestStreamerCommittedTranscript` — mock server sends `committed_transcript` messages, verify they appear on the `Committed` channel and are written to the transcript file
- [ ] `TestStreamerPartialTranscript` — mock server sends `partial_transcript`, verify it appears on `Partial` channel
- [ ] `TestStreamerIncrementalFileWrite` — send multiple committed transcripts, verify file contains all of them after each one (read file between sends)
- [ ] `TestStreamerCleanStop` — call `Stop()`, verify WebSocket closes cleanly
- [ ] `TestStreamerErrorHandling` — mock server sends an error message type, verify it appears on `Err` channel
- [ ] Run tests, confirm they all fail (no implementation yet)
- [ ] Commit: "test: add streamer tests with mock WebSocket server"

### Task 2.2: Implement Streamer

Create `internal/transcribe/stream.go`:

```go
package transcribe

type Streamer struct {
    apiKey       string
    model        string
    storeInCloud bool
    baseURL      string  // "wss://api.elevenlabs.io" default, overridable for tests

    Committed chan string
    Partial   chan string
    Err       chan error

    conn         *websocket.Conn
    cancel       context.CancelFunc
    mu           sync.Mutex
    committed    []string       // accumulated committed text
    file         *os.File       // transcript file, flushed on each commit
    writer       *bufio.Writer
}
```

- [ ] Implement `NewStreamer(apiKey, model string, storeInCloud bool) *Streamer` — allocate channels (buffered: Committed 64, Partial 16, Err 1), set defaults
- [ ] Implement `Start(ctx context.Context, pcmReader io.Reader, transcriptPath string) error`:
  - Build WebSocket URL with query params: `model_id`, `commit_strategy=vad`, `vad_silence_threshold_secs=1`, `audio_format=pcm_16000`
  - Set `xi-api-key` header
  - Dial WebSocket via `websocket.DefaultDialer.Dial`
  - Open transcript file for append+create
  - Spawn `sendLoop` goroutine: reads 4096-byte chunks from `pcmReader`, base64-encodes, sends as JSON `input_audio_chunk` message. On reader EOF or context cancel, return.
  - Spawn `recvLoop` goroutine: reads JSON messages from WebSocket, dispatches by `message_type`:
    - `session_started`: log/ignore
    - `partial_transcript`: send text on `Partial` channel (non-blocking)
    - `committed_transcript`: append to `committed`, send on `Committed` channel, write to file + flush
    - Error types (`error`, `auth_error`, `quota_exceeded`, etc.): send on `Err` channel
  - Return nil on success
- [ ] Implement `Stop()`: cancel context, close WebSocket with close message, close file, close channels
- [ ] Implement `FullText() string`: join committed with spaces
- [ ] Run tests from 2.1, confirm they pass
- [ ] Run full test suite
- [ ] Commit: "feat: implement ElevenLabs realtime WebSocket streamer"

---

## Phase 3: Transcript Viewport Widget

### Task 3.1: Write transcript viewport tests

Create `internal/tui/transcript_test.go`:

- [ ] `TestTranscriptViewportContentBuild` — committed text + dim partial produces correct string
- [ ] `TestTranscriptViewportAutoScroll` — after adding content, viewport is at bottom; after scrolling up, autoScroll is false; after scrolling back to bottom, autoScroll is true
- [ ] `TestTranscriptViewportWordWrap` — long text wraps at viewport width
- [ ] `TestTranscriptViewportLiveIndicator` — when scrolled up, `↓ live` indicator appears
- [ ] Run tests, confirm they fail
- [ ] Commit: "test: add transcript viewport widget tests"

### Task 3.2: Implement transcript viewport

Create `internal/tui/transcript.go`:

```go
package tui

type TranscriptViewport struct {
    viewport    viewport.Model
    committed   string     // accumulated committed text
    partial     string     // current partial (dim)
    autoScroll  bool
    width       int
    height      int
}

func NewTranscriptViewport(width, height int) TranscriptViewport
func (tv *TranscriptViewport) AppendCommitted(text string)
func (tv *TranscriptViewport) SetPartial(text string)
func (tv *TranscriptViewport) SetSize(width, height int)
func (tv *TranscriptViewport) Update(msg tea.Msg) (*TranscriptViewport, tea.Cmd)
func (tv *TranscriptViewport) View() string
func (tv *TranscriptViewport) IsAutoScroll() bool
```

- [ ] `NewTranscriptViewport`: create `viewport.New(width, height)`, set `autoScroll = true`
- [ ] `AppendCommitted`: append text + space to `committed`, rebuild content, if `autoScroll` call `viewport.GotoBottom()`
- [ ] `SetPartial`: set `partial`, rebuild content (committed + dim partial at end)
- [ ] Content rebuild: word-wrap `committed + partialStyled` to `width`, call `viewport.SetContent()`
- [ ] Word wrapping: split on spaces, track line length, insert newlines at width boundary. Use lipgloss dim style for partial text.
- [ ] `Update`: delegate key/mouse messages to `viewport.Update()`. After update, check `viewport.AtBottom()` — if true set `autoScroll = true`, if user scrolled up set `autoScroll = false`
- [ ] `View`: render `viewport.View()`. If `!autoScroll`, overlay `↓ live` in dim style at bottom-right corner
- [ ] `SetSize`: update viewport dimensions
- [ ] Run tests, confirm they pass
- [ ] Commit: "feat: implement transcript viewport widget with auto-scroll"

---

## Phase 4: TUI Integration

### Task 4.1: Add streamer messages and listeners to TUI model

Modify `internal/tui/model.go`:

- [ ] Add new message types:
  ```go
  type committedMsg string
  type partialMsg string
  type streamErrMsg error
  ```
- [ ] Add listener commands (same pattern as `listenLevel`):
  ```go
  func listenCommitted(s *transcribe.Streamer) tea.Cmd
  func listenPartial(s *transcribe.Streamer) tea.Cmd
  func listenStreamErr(s *transcribe.Streamer) tea.Cmd
  ```
  Each blocks on the respective channel, returns the message type.
- [ ] Add fields to `Model`:
  ```go
  streamer           *transcribe.Streamer
  transcript         TranscriptViewport
  liveTranscription  bool
  streamErr          error  // if streaming failed, for fallback decision
  ```
- [ ] Add constructor: `NewModelWithStreamer(rec, opts, streamer)` — sets `liveTranscription = true`, creates `TranscriptViewport`, sets waveform height to 5 via `NewAnimation(60, 5)`
- [ ] In `Init()`: when `liveTranscription`, batch the three listener commands alongside existing ones
- [ ] Commit: "feat: add streamer message types and listeners to TUI model"

### Task 4.2: Handle streamer messages in Update

- [ ] In `Update`, add cases:
  - `committedMsg`: call `m.transcript.AppendCommitted(string(msg))`, re-listen on committed channel
  - `partialMsg`: call `m.transcript.SetPartial(string(msg))`, re-listen on partial channel
  - `streamErrMsg`: set `m.streamErr = error(msg)`, log warning to stderr, set `m.liveTranscription = false` (hide viewport, stop listeners). Don't quit — recording continues, will fall back to batch.
- [ ] In key handling: delegate arrow keys, page up/down, End, mouse scroll to `m.transcript.Update(msg)` when `liveTranscription` is true. Be careful not to conflict with existing keybindings (q, Q, m, space, ctrl+c). Arrow up/down and pgup/pgdn are currently unused so they're safe.
- [ ] In `WindowSizeMsg`: call `m.transcript.SetSize(width, viewportHeight)` where `viewportHeight = height - fixedChrome` (13 lines, min 4)
- [ ] Commit: "feat: handle streamer messages and viewport scrolling in TUI"

### Task 4.3: Update View for transcript viewport

- [ ] In `View()`: when `liveTranscription` is true:
  - Use waveform height 5 (already set via constructor animation size)
  - After mic/out lines, render a separator: `dimStyle.Render(strings.Repeat("─", width))`
  - Render `m.transcript.View()` filling remaining height
  - Update key hints to include scroll: `[↑↓] scroll  [m]ute  [q]uit  [Q]uit+transcribe`
- [ ] When `liveTranscription` is false, render exactly as before (no viewport, 9-row waveform)
- [ ] Commit: "feat: render transcript viewport in TUI with separator and scroll hints"

---

## Phase 5: Command Wiring

### Task 5.1: Wire up streamer in record command

Modify `cmd/record.go`:

- [ ] Add import for `transcribe` package
- [ ] In `runRecord`, after config loading and before recording starts, check if live transcription should be activated:
  ```go
  liveTranscribe := rTranscribe  // -t flag
  var streamer *transcribe.Streamer
  if liveTranscribe {
      cfg.ApplyEnv()
      if cfg.Transcribe.ElevenLabs.APIKey != "" {
          streamer = transcribe.NewStreamer(
              cfg.Transcribe.ElevenLabs.APIKey,
              cfg.Transcribe.ElevenLabs.Model,
              cfg.Transcribe.ElevenLabs.StoreInCloud,
          )
      }
  }
  ```
- [ ] When `streamer != nil`, set `opts.LivePCM = true` on `RecordOpts` before calling `record.Start()`
- [ ] After `record.Start()` succeeds and `streamer != nil`:
  - Compute transcript path: `transcriptPathFor(outputPath, transcribe.FormatText)` (reuse from cmd/transcribe.go — may need to export or inline)
  - Call `streamer.Start(ctx, rec.PCMReader, transcriptPath)`
  - If start fails: log warning, set `streamer = nil`, continue without live transcription
- [ ] When creating the TUI model: if `streamer != nil`, use `tui.NewModelWithStreamer(rec, opts, streamer)` instead of `tui.NewModel(rec, opts)`
- [ ] After TUI exits and recording stops: if `streamer != nil`, call `streamer.Stop()`
- [ ] For `shouldTranscribe` logic: if `streamer != nil` and `model.streamErr == nil`, skip `runPostTranscribe` (transcript already saved). If `streamErr != nil`, fall back to batch.
- [ ] Commit: "feat: wire up live streaming transcription in record command"

### Task 5.2: Handle clips mode

- [ ] In `runClips`: same pattern — when `-t` and ElevenLabs available, create streamer per clip, start/stop with each clip's recording lifecycle
- [ ] Commit: "feat: support live transcription in clips mode"

### Task 5.3: Export transcriptPathFor

- [ ] In `cmd/transcribe.go`, the `transcriptPathFor` function is unexported. Either export it or move to a shared location so `cmd/record.go` can use it. Since both files are in package `cmd`, it's already accessible. No change needed — verify this.
- [ ] Commit (if needed): "refactor: share transcriptPathFor between record and transcribe commands"

---

## Phase 6: Integration Testing

### Task 6.1: Integration test with mock WebSocket

- [ ] In `integration_test.go`, add test: `TestRecordWithLiveTranscription`
  - Start a mock WebSocket server that echoes committed transcripts
  - Set `ELEVENLABS_API_KEY=test` env var
  - Configure the streamer's base URL to point at mock server (may need a config override or env var for base URL)
  - Run `record -t --no-tui -d 2s` with a virtual audio source or very short duration
  - Verify transcript file exists and contains committed text
- [ ] Run full test suite
- [ ] Commit: "test: add integration test for live streaming transcription"

---

## Phase 7: Polish and Docs

### Task 7.1: Update README

- [ ] Add `--transcribe` / `-t` description mentioning live streaming when ElevenLabs is configured
- [ ] Document the live transcript TUI: scrolling, auto-scroll, `↓ live` indicator
- [ ] Mention incremental file saving (crash-safe)
- [ ] Commit: "docs: document live streaming transcription in README"

### Task 7.2: Update config.example.toml

- [ ] Add comment about live transcription support under `[transcribe.elevenlabs]`
- [ ] Commit: "docs: note live transcription in example config"

### Task 7.3: Final verification

- [ ] Run `go build ./...`
- [ ] Run `go test ./...`
- [ ] Manual test: `record -t` with real ElevenLabs key, verify live text appears, scrolling works, transcript file saved
- [ ] Commit any final fixes
