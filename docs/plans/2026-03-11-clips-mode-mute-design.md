# Clips Mode & Mute Design

## Overview

Add a "clips" recording mode where the user can record multiple sequential clips in one session, and replace pause with per-stream mute functionality.

## CLI Changes

- Change `Args` from `cobra.MaximumNArgs(1)` to `cobra.ArbitraryArgs` — multiple positional words are joined with `_` to form the recording label. Applies to all modes (e.g., `record my cool meeting` → `my_cool_meeting-2026-03-11T14-30-00.ogg`).
- Add `--clips` / `-C` bool flag to enable clips mode. Requires a name (error if no positional args and no `-n` flag).

## Clip Filename Format

`{name}-{NNN}-{timestamp}.{format}`

Example: `interview-001-2026-03-11T14-30-00.ogg`, `interview-002-2026-03-11T14-31-22.ogg`

All clips saved in the root recording directory (no subfolders).

## Mute (All Modes)

Replaces pause functionality entirely. `p`/`space` no longer pause.

### Implementation

- `m` key toggles mute
- Uses `pactl set-source-output-mute` on ffmpeg's specific PulseAudio source-output (per-stream, not system-wide)
- Recorder finds ffmpeg's source-output ID by matching PID via `pactl list source-outputs short` after start
- Recorder exposes `ToggleMute()` and `IsMuted() bool`

### TUI

- Shows muted indicator in status line when muted (yellow, like old pause style)
- Waveform goes flat while muted

## Clips Mode Flow

### Recording Loop (in `runRecord`)

1. Generate filename with clip number and timestamp
2. Start recorder, launch TUI
3. On `q`: stop recorder, save file, print path, increment clip counter, loop back to step 1 (new TUI starts in `StateReady`)
4. On `Q`: stop, save, transcribe all saved clips, exit
5. On `ctrl+c`: stop, save, exit

Each iteration creates a fresh `Recorder` and `Model` / `tea.Program`. The first clip auto-starts recording; subsequent clips start in `StateReady`.

### TUI States

New `StateReady` state added, used between clips:

```
  ⏳ READY   00:00:00

  [waveform area - flat/empty]

  ✓ Saved clip 3!
  mic: ...
  out: (next clip path)

  [space] record  [q]uit  [Q]uit+transcribe
```

### Model Changes

New fields:
- `clipsMode bool`
- `clipNumber int` (current clip, for display)
- `clipsSaved int` (total saved so far)
- `savedMessage string` (e.g., "Saved clip 3!")

New methods:
- `ClipDone() bool` — caller checks this to decide whether to loop

### Key Bindings

**Regular mode:**
| Key | Action |
|-----|--------|
| `m` | Toggle mute |
| `q` | Stop and save |
| `Q` | Stop, save, transcribe |
| `ctrl+c` | Stop and save |

**Clips mode (recording):**
| Key | Action |
|-----|--------|
| `m` | Toggle mute |
| `q` | Save clip → ready state |
| `Q` | Save clip → transcribe all → quit |
| `ctrl+c` | Save clip → quit |

**Clips mode (ready state):**
| Key | Action |
|-----|--------|
| `space` / `m` | Start next clip |
| `q` | Quit |
| `Q` | Transcribe all saved clips → quit |
| `ctrl+c` | Quit |
