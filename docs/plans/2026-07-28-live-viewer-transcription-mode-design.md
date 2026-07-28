# Live Viewer Transcription Mode — Design

Date: 2026-07-28
Status: Approved

## Goal

Rework the recording TUI to behave like Claude Code's dictation mode: the live
transcript is the main content of the screen, and the "cursor" at the text
insertion point is a single-cell VU meter that changes height and color with
the mic level. The big waveform animation is removed entirely. Live
transcription becomes always-on whenever an ElevenLabs API key is configured,
in both single-recording and clips mode.

## UX summary

- `record` always live-transcribes when `cfg.Transcribe.ElevenLabs.APIKey` is
  set — no `-t` flag needed. The recording screen is transcript-first; the
  waveform animation is gone in every mode.
- **`q`** — stop and save. The live transcript is *promoted*: copied from
  `<base>-live.txt` to `<base>.txt`, so a canonical transcript always exists.
- **`Q`** — same promote, then the batch (Scribe) retranscription runs and
  overwrites `<base>.txt` with the diarized, higher-quality result. If batch
  fails, the promoted live transcript survives as the fallback.
- **`-t/--transcribe`** — now means "auto-batch on exit" (equivalent to
  pressing `Q`), which is backward compatible with existing scripts. Without
  `-t`, batch runs only when the user presses `Q`.
- **`--no-tui`** — headless runs also stream live, promote on completion, and
  batch if `-t`.
- **Clips mode (`--clips`)** — full parity: a fresh streamer per clip writes
  `<name>_clipN-live.txt`; saving a clip (`q`) promotes it to
  `<name>_clipN.txt`; `Q` ends the session and batch-retranscribes all saved
  clips (overwriting each promoted `.txt`).

## Screen layout (single + clips, identical)

```
● REC 00:42 · clip 2      48kHz stereo   -23 dB
mic: Built-in Microphone
out: ~/memos/standup_clip2.ogg
────────────────────────────────────────────
So the main thing I wanted to cover today is
the release timeline. We pushed the beta back
a week because of the… and we're look▅
────────────────────────────────────────────
[↑↓] scroll  [m]ute  [q]uit  [Q]uit+transcribe
```

- The transcript viewport fills all remaining terminal height. Fixed
  (non-transcript) rows shrink from 13 to ~7.
- The dB readout moves into the header line, right-aligned, in the existing
  dim style (`#666666`), fed by the same smoothed level as before.
- Scroll keys (`↑`/`↓`/`pgup`/`pgdown`/`end`), auto-scroll engage/disengage,
  and the `↓ live` indicator are unchanged.
- `internal/tui/animation.go` (and its tests) are deleted. The attack/decay
  smoothing it contained moves into a small `VUMeter` type in
  `internal/tui/vu.go`, which drives both the dB readout and the VU cursor.

## The VU cursor

A single cell appended immediately after the last character of the transcript
text — end of the dim partial text while an utterance is in progress, or end
of the committed text between utterances. On an empty transcript it sits alone
at the top-left of the transcript area.

- **Height:** rune from `▁▂▃▄▅▆▇█` selected by the smoothed 0–1 level.
  Silence floors at `▁` — the cursor is never invisible.
- **Color:** existing thresholds — green `#22c55e` below 0.6, yellow
  `#eab308` below 0.85, red `#ef4444` at/above 0.85. When muted or in the
  clips READY state: static dim-gray `▁`.
- **Update rate:** the existing 33 ms (~30 fps) tick already re-renders every
  frame; each render reads the latest smoothed level. Smoothing keeps the
  fast-attack (0.5) / slow-decay (0.15) feel from the old waveform's dB path.
- **Wrapping:** `wrapTranscript` reserves one cell on the final line so the
  cursor always hugs the text and never wraps onto a line by itself.

## No-stream fallback

Same layout everywhere; there is no waveform fallback path.

- No API key, or the stream fails to start: the transcript area shows the
  lone VU cursor plus a dim note — `live transcription unavailable: <reason>`.
- Stream dies mid-recording: accumulated text stays, the cursor keeps
  metering at the end of it, and the existing amber
  `⚠ live transcription stopped: …` line appears.
- Recording is never interrupted by stream problems. The existing PCM-drain
  behavior (draining `rec.PCMReader` when the streamer fails to start so
  ffmpeg doesn't block) is preserved.

## Wiring changes (`cmd/record.go`)

- Create the streamer whenever the API key exists — drop the `rTranscribe`
  gate. Set `RecordOpts.LivePCM` accordingly.
- After `rec.Wait()` and `streamer.Stop()`: promote `<base>-live.txt` →
  `<base>.txt`. Skip if the live file is missing or empty. A promote failure
  is a stderr warning, never fatal. Batch (`runPostTranscribe`) runs after the
  promote when `-t` was passed or the TUI reports `ShouldTranscribe()`.
- `runClips` gets the same wiring per clip: a fresh `Streamer` per clip
  (streamers are single-use — `Stop` closes their channels), started against
  `<clipbase>-live.txt`, promoted to `<clipbase>.txt` on each save. `Q`
  batch-retranscribes all saved clips at session end. A failed stream on one
  clip falls back to the lone-cursor UI for that clip only; the next clip
  creates a new streamer and retries.
- The clips model constructor grows a streamer hook (mirroring
  `NewModelWithStreamer`) so each clip's model can consume
  `Committed`/`Partial`/`Err` channels.
- Update `record --help` and the `-t` flag description for the new semantics.

## TUI model consolidation

One layout for all modes. The `liveTranscription` flag no longer selects a
different layout — it only reflects whether a streamer is feeding the
transcript. `NewModel` (no streamer) renders the same screen with an empty
transcript, the lone VU cursor, and the unavailable note.

## Error handling

- Stream start failure: warn to stderr, drain PCM, continue with fallback UI.
- Mid-recording fatal stream error: amber warning line, recording continues.
- Promote failure (copy error): stderr warning, exit code unaffected.
- Batch failure after `Q`/`-t`: error surfaces as today; the promoted live
  transcript remains at `<base>.txt`.

## Testing

- Unit tests:
  - VU cursor: level → rune mapping, level → color thresholds, muted/READY
    rendering.
  - `wrapTranscript`: cursor placement with empty transcript, partial-only,
    committed+partial, and the exact-width boundary (reserved cell prevents a
    lone-wrapped cursor).
  - Promote logic: live file copied to canonical path; missing/empty live
    file skipped; batch-overwrites-after-promote ordering.
  - Clips: per-clip promote naming (`_clipN-live.txt` → `_clipN.txt`).
- Update existing transcript viewport tests for the new render path; delete
  animation tests with `animation.go`.
- Review `integration_test.go` for assumptions about the waveform or the old
  `-t` gating and update accordingly.

## Out of scope

- Any change to the batch transcription backends or output formats.
- Config options for choosing the old waveform view (it is removed, not
  optional).
- Streaming backends other than ElevenLabs realtime.
