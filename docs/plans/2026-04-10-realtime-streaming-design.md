# Realtime Streaming Transcription Design

## Summary

Add live transcription during recording via ElevenLabs realtime WebSocket API.
When `record -t` is used (or `Q` is pressed during recording) and an ElevenLabs
key is configured, audio is streamed to ElevenLabs for live speech-to-text.
Transcript text appears in a scrollable viewport below the waveform as the user
speaks. The transcript file is written incrementally so no data is lost on crash.

## Trigger

- `record -t` or pressing `Q` during recording
- Requires ElevenLabs API key configured
- If no ElevenLabs key, falls back to batch transcription after recording (existing behavior)

## Audio Pipeline

Modify `BuildFFmpegArgs` to add a second output when live transcription is active:

- Primary output: encoded file (ogg/wav/etc) as today
- Secondary output: raw `pcm_s16le` at 16kHz mono written to a pipe fd (`pipe:3` or similar)

A goroutine reads fixed-size PCM chunks (e.g. 4096 bytes = 128ms at 16kHz 16-bit mono)
from the pipe and sends them as base64-encoded `input_audio_chunk` messages over the
WebSocket.

FFmpeg additional args: `-f s16le -ar 16000 -ac 1 pipe:3` (using an extra fd opened
before exec, or a named pipe / os.Pipe).

## WebSocket Protocol

Connect to: `wss://api.elevenlabs.io/v1/speech-to-text/realtime`

Query parameters:
- `model_id=scribe_v2`
- `commit_strategy=vad`
- `vad_silence_threshold_secs=1`
- `audio_format=pcm_16000`

Headers:
- `xi-api-key: <api_key>`

### Client sends

```json
{
  "message_type": "input_audio_chunk",
  "audio_base_64": "<base64 PCM data>",
  "commit": false,
  "sample_rate": 16000
}
```

### Server sends

- `session_started` -- connection established, config confirmed
- `partial_transcript` -- in-progress text (may change), shown dim in TUI
- `committed_transcript` -- finalized text (won't change), shown solid in TUI
- Various error types -- handled gracefully, fall back to batch

## TUI Layout

When live transcription is active, the layout adapts to fill the terminal:

```
  ● REC  00:01:23       48kHz mono        (fixed: 1 line)
                                           (spacer: 1 line)
  [waveform, 5 rows]  -12.3 dB            (fixed: 5 lines)
                                           (spacer: 1 line)
  mic: ...                                 (fixed: 1 line)
  out: ...                                 (fixed: 1 line)
  ─────────────────────────────────────    (fixed: 1 line)
  committed text flows here, wrapping      (dynamic: fills remaining)
  and scrolling as more arrives. older
  text scrolls up and off the top.
  partial text shown dim here_
                                     ↓ live (when scrolled up)
  [m]ute  [q]uit  [Q]uit+transcribe       (fixed: 1 line)
```

Fixed chrome: ~13 lines. Transcript viewport: `terminal_height - 13` lines (minimum 4).

Waveform height: 5 rows when live transcription is active (down from 9 in non-live mode).

### Scrolling behavior

- Uses `bubbles/viewport` for the transcript area
- **Auto-scroll (default):** pinned to bottom, new text appears as it arrives
- **Browse mode:** arrow up / Page Up / mouse scroll up detaches from auto-scroll,
  user can freely browse transcript history
- **Re-attach:** scrolling past the bottom, or pressing End, re-enables auto-scroll
- **Indicator:** when scrolled up (not at bottom), show `↓ live` in dim style at the
  bottom-right of the transcript area to indicate newer text exists below

### Text display

- Committed text: normal foreground color, word-wrapped to terminal width
- Partial text: dim/gray style, appended after committed text, updates in place
- New committed text replaces the partial and a newline is implicit between utterances

## Incremental File Saving

The transcript file (e.g. `recording-2026-04-10T14-30-05.txt`) is opened at the
start of live transcription and kept open for the session.

- On each `committed_transcript` message: append text + newline, flush to disk
- On clean exit: close file (already complete)
- On crash/kill: at most the current partial (uncommitted) text is lost;
  all committed segments are already on disk

The file handle is managed by the streaming transcriber, not the TUI.

## Architecture

### New package: `internal/transcribe/stream.go`

```go
// Streamer manages a realtime ElevenLabs WebSocket transcription session.
type Streamer struct {
    apiKey       string
    model        string
    storeInCloud bool
    // Channels for TUI consumption
    Committed chan string   // finalized text segments
    Partial   chan string   // in-progress text (replaced on each update)
    Err       chan error    // fatal errors
}

func NewStreamer(apiKey, model string, storeInCloud bool) *Streamer
func (s *Streamer) Start(ctx context.Context, pcmReader io.Reader, transcriptPath string) error
func (s *Streamer) Stop()
func (s *Streamer) FullText() string  // returns all committed text joined
```

- `Start` connects the WebSocket, spawns goroutines for reading PCM + sending chunks
  and receiving transcript messages
- Committed text is written to the transcript file incrementally
- `Stop` closes the WebSocket cleanly
- `FullText` returns the accumulated transcript for post-recording use

### Changes to `internal/record/recorder.go`

New function `BuildFFmpegArgsWithPCMPipe` (or modify existing) that adds the PCM
pipe output. The `Recorder` struct gains:

```go
PCMReader io.ReadCloser  // read end of PCM pipe, nil when not streaming
```

When live transcription is requested, `Start` creates an `os.Pipe()`, passes the
write end as an extra fd to ffmpeg, and exposes the read end as `PCMReader`.

### Changes to `internal/tui/model.go`

New fields on `Model`:
- `streamer *transcribe.Streamer`
- `viewport viewport.Model` (from bubbles)
- `committedText string` -- accumulated committed transcript
- `partialText string` -- current partial
- `autoScroll bool` -- whether to pin to bottom
- `liveTranscription bool` -- whether streaming is active

New messages:
- `committedMsg string` -- received committed transcript
- `partialMsg string` -- received partial transcript
- `streamErrMsg error` -- streaming error

Listener commands (like `listenLevel`) for the Committed/Partial/Err channels.

View changes:
- When `liveTranscription` is true, reduce waveform height to 5 and render transcript
  viewport below the separator
- Viewport content = committed text + dim partial text, word-wrapped
- Auto-scroll logic: after appending text, if `autoScroll`, call `viewport.GotoBottom()`
- On scroll up: set `autoScroll = false`
- On scroll to/past bottom: set `autoScroll = true`
- Show `↓ live` indicator when `!autoScroll`

### Changes to `cmd/record.go`

When `-t` is passed and ElevenLabs is configured:
1. Create streamer
2. Pass `startFunc` or modify `Start` to create PCM pipe
3. Start streamer with PCM reader and transcript path
4. Pass streamer to TUI model
5. On exit: stop streamer, skip `runPostTranscribe` (transcript already saved)
6. On streamer error: log warning, fall back to batch transcription after recording

### Changes to `cmd/transcribe.go`

No changes needed -- batch transcription is unaffected.

## New Dependency

`github.com/gorilla/websocket` for the WebSocket client.

## Error Handling

- WebSocket connection failure: log warning, fall back to batch transcription
- WebSocket disconnect mid-stream: log warning, save what we have, fall back to batch
  for the remainder (re-transcribe full file after recording)
- ElevenLabs error messages (quota, rate limit, etc.): show in TUI as warning, fall
  back to batch
- PCM pipe broken (ffmpeg dies): streamer stops, recording stops (existing behavior)

## Testing

- Unit tests for `Streamer`: mock WebSocket server, verify chunk sending, committed/partial
  channel delivery, file writing, cleanup
- Unit tests for TUI model: verify viewport scrolling, auto-scroll behavior, text
  accumulation from committed/partial messages
- Integration test: verify `record -t` with mock ElevenLabs WebSocket produces transcript file
