# Device Management, Diarization & Multi-Device Recording

## Overview

Three interconnected features for audiomemo:

1. **Diarization & transcription options** — per-backend flags (`--diarize`, `--smart-format`, `--punctuate`) with strict validation
2. **Device aliases, groups & defaults** — named devices and multi-device groups stored in XDG config
3. **Device management TUI** — full-screen bubbletea interface for browsing devices, creating aliases/groups, live VU preview, and test recording

## 1. Config Schema Changes

The TOML config at `$XDG_CONFIG_HOME/audiomemo/config.toml` gains three new sections:

```toml
[record]
format = "ogg"
sample_rate = 48000
channels = 1
output_dir = "~/Recordings"
device = "mic"  # can reference an alias, a group, or a raw device name

[devices]
mic = "alsa_input.usb-Blue_Microphones-00.mono-fallback"
desktop = "alsa_output.pci-0000_0c_00.4.analog-stereo.monitor"

[device_groups]
zoom = ["mic", "desktop"]

[transcribe]
default_backend = ""
language = ""
output_format = "text"

[transcribe.whisper]
model = "base"
binary = "whisper"
hf_token = ""       # for whisperx diarization
diarize = false      # only valid when variant is whisperx

[transcribe.deepgram]
api_key = ""
model = "nova-3"
diarize = true
smart_format = true
punctuate = true

[transcribe.openai]
api_key = ""
model = "gpt-4o-transcribe"

[transcribe.mistral]
api_key = ""
model = "voxtral-mini-latest"
```

### Key rules

- `[devices]` — flat key-value map: alias name to raw PulseAudio/AVFoundation source name.
- `[device_groups]` — map of group name to list of alias names.
- `record.device` — resolved in order: check `[device_groups]`, then `[devices]`, then treat as raw device name, then fall back to system default.
- Backend-specific options (`diarize`, `smart_format`, `punctuate`) live under their backend section, not top-level `[transcribe]`.

### Config struct changes

```go
type Config struct {
    Record       RecordConfig            `toml:"record"`
    Devices      map[string]string       `toml:"devices"`
    DeviceGroups map[string][]string     `toml:"device_groups"`
    Transcribe   TranscribeConfig        `toml:"transcribe"`
}

type WhisperConfig struct {
    Model   string `toml:"model"`
    Binary  string `toml:"binary"`
    HFToken string `toml:"hf_token"`
    Diarize bool   `toml:"diarize"`
}

type DeepgramConfig struct {
    APIKey      string `toml:"api_key"`
    Model       string `toml:"model"`
    Diarize     bool   `toml:"diarize"`
    SmartFormat bool   `toml:"smart_format"`
    Punctuate   bool   `toml:"punctuate"`
}
```

A new `config.Save()` / `config.SaveTo(path)` method writes the config back to TOML, round-tripping cleanly.

## 2. Diarization & Transcription Options

### New CLI flags

```
audiomemo transcribe [flags] <file>
  --diarize          enable speaker diarization
  --smart-format     apply smart formatting (Deepgram)
  --punctuate        add punctuation (Deepgram)
```

### Validation

Both CLI flags and config-level defaults error on unsupported backends. If `--diarize` is passed (or `deepgram.diarize = true` in config) and the active backend doesn't support it, the backend returns an error:

```
error: whisper-cpp does not support --diarize
```

This forces users to put backend-specific options in the correct backend section and prevents silent misconfiguration.

### Backend support matrix

| Flag             | Deepgram           | whisperx              | OpenAI | Mistral | whisper-cpp | ffmpeg-whisper |
|------------------|--------------------|-----------------------|--------|---------|-------------|----------------|
| `--diarize`      | `diarize=true` param | `--diarize` flag     | error  | error   | error       | error          |
| `--smart-format` | `smart_format=true`  | error               | error  | error   | error       | error          |
| `--punctuate`    | `punctuate=true`     | error               | error  | error   | error       | error          |

### TranscribeOpts changes

```go
type TranscribeOpts struct {
    Model       string
    Language    string
    Format      OutputFormat
    Verbose     bool
    Diarize     bool       // new
    SmartFormat bool       // new
    Punctuate   bool       // new
}
```

Opts are populated by merging config defaults (from the active backend's section) with CLI flag overrides. The backend's `Transcribe()` method validates that it supports the requested options before proceeding.

### Result struct changes

```go
type Segment struct {
    Start   float64 `json:"start"`
    End     float64 `json:"end"`
    Text    string  `json:"text"`
    Speaker string  `json:"speaker,omitempty"`  // new
}
```

### Output format rendering with speakers

- **text**: `Speaker 1: Hello there.\nSpeaker 2: Hi!`
- **json**: `"speaker": "Speaker 1"` field on each segment
- **srt**: `[Speaker 1]` prefix before subtitle text
- **vtt**: `[Speaker 1]` prefix before subtitle text

When diarization is not enabled or the backend doesn't return speaker info, the `Speaker` field is empty and output formats render without prefixes (no behavior change).

### whisperx HuggingFace token

whisperx diarization requires a HuggingFace token. Resolved from:
1. `transcribe.whisper.hf_token` in config
2. `HF_TOKEN` environment variable

Error if diarization is requested but no token is available.

## 3. Multi-Device Recording

### Resolution flow

When `record.device` references a group (or `-D groupname` is passed):

1. Look up group in `config.DeviceGroups` to get list of alias names.
2. Resolve each alias via `config.Devices` to raw device names.
3. Unknown alias in group = error at startup before recording begins.
4. Group with 1 device = treated as single device recording.

### FFmpeg multi-input

For a group with N devices, build ffmpeg args with multiple inputs mixed to mono:

```
ffmpeg -f pulse -i "mic_raw_name" \
       -f pulse -i "monitor_raw_name" \
       -filter_complex "[0:a][1:a]amix=inputs=2:duration=longest[a]" \
       -map "[a]" \
       -af "asetnsamples=n=480,astats=metadata=1:reset=1,ametadata=print:file=-" \
       -c:a libopus -ar 48000 -ac 1 \
       -output_ts_offset 0 -y output.ogg
```

Note: the `astats` VU filter runs on the mixed output, so the TUI shows the combined level.

### Implementation

- `record.BuildFFmpegArgs` is extended to accept `[]string` devices. Single device is `[]string{device}` (no behavior change).
- New `record.BuildFFmpegArgsMulti(devices []string, opts RecordOpts) []string` handles the multi-input `filter_complex` construction.
- `cmd/record.go` resolves device/alias/group before calling `record.Start()`.
- The TUI `mic:` line shows the group: `mic: zoom (mic + desktop)`.

### Edge cases

- Group with 1 device: no `filter_complex`, same as single device.
- Group with 3+ devices: `amix=inputs=N`.
- Empty group: error.
- Alias not found in group: error at startup.
- Monitor device in group: works (PulseAudio monitor sources are valid ffmpeg inputs).

## 4. Device Management TUI

### Invocation

```
audiomemo device       # launch device manager TUI
```

New cobra subcommand in `cmd/device.go`. Also accessible from the recording TUI via the existing `[d]evices` keybinding.

Non-interactive fallbacks for scripts:

```
audiomemo device list                    # print all devices
audiomemo device alias mic "Blue Yeti"   # create alias
audiomemo device group zoom mic,desktop  # create group
audiomemo device default mic             # set default
```

### TUI state machine

```
Browse -> [a] -> AliasPrompt  -> Browse
       -> [g] -> GroupEdit    -> Browse
       -> [d] -> SetDefault   -> Browse
       -> [t] -> TestRecord   -> TestPlayback -> Browse
       -> [x] -> ConfirmDelete -> Browse
       -> [q] -> Save & Quit
```

### Layout

```
+-- Devices ----------------------------+-- Config ----------------+
|  SOURCES                              |  ALIASES                 |
|  > Blue Yeti USB                 [mic]|  mic -> Blue Yeti USB    |
|    Built-in Microphone                |  desktop -> analog.mon.. |
|                                       |                          |
|  MONITORS                             |  GROUPS                  |
|    analog-stereo.monitor     [desktop]|  zoom -> mic, desktop    |
|    hdmi-stereo.monitor                |                          |
|                                       |  DEFAULT: mic            |
+---------------------------------------+--------------------------+
|  ██████░░░░░░░░░░ -18.4 dB  Blue Yeti USB                       |
+------------------------------------------------------------------+
|  [a]lias  [g]roup  [d]efault  [t]est  [x]delete  [q]uit         |
+------------------------------------------------------------------+
```

### Device browser (left panel)

- Two sections: **Sources** (physical inputs) and **Monitors** (output loopbacks).
- PulseAudio: monitors detected by `.monitor` suffix on source name. Could also use `pactl list sources` for richer metadata.
- macOS: AVFoundation enumeration (different detection).
- Arrow keys navigate. Selected device is highlighted.
- Devices with existing aliases show the alias tag inline, e.g., `[mic]`.

### Live VU preview (bottom bar)

- Reuses the horizontal `VUMeter` component for the preview bar.
- When a device is selected, a short-lived ffmpeg subprocess captures audio from that device and pipes RMS levels to the VU meter.
- The ffmpeg subprocess is killed and restarted when the selection changes.
- Shows: VU bar + smoothed dB readout + device description.

### Alias creation (`a` key)

1. Text input prompt: "Alias name:"
2. Validates: no spaces, no collision with existing alias/group names.
3. Maps the alias to the currently selected device's raw name.
4. Writes to config immediately.
5. The `[alias]` tag appears next to the device in the browser.

### Group creation (`g` key)

1. Text input prompt: "Group name:"
2. Switches to a multi-select view of existing aliases.
3. Space to toggle aliases in/out of the group.
4. Enter to confirm.
5. Writes to config.

### Set default (`d` key)

Sets `record.device` in config to the selected device's alias (if it has one) or raw name.

### Test recording (`t` key)

1. Status bar: "Recording 3s test..."
2. Records 3 seconds from selected device to a temp file using the standard ffmpeg pipeline.
3. Status bar: "Playing back..."
4. Plays via `ffplay -nodisp -autoexit tempfile.ogg` (fallback: `paplay`).
5. Cleans up temp file, returns to browse.

### Delete (`x` key)

- If selected item is an aliased device: confirm, then remove alias from `[devices]` and from any groups that reference it.
- If selected item in the config panel is a group: confirm, then remove group from `[device_groups]`.
- If the deleted alias was the default device, clear `record.device`.

### Device discovery

Extend `record.Device` struct and `ListDevices()`:

```go
type Device struct {
    Name        string
    Description string
    IsDefault   bool
    IsMonitor   bool   // true for *.monitor sources
    SampleRate  int    // native sample rate (if discoverable)
    Channels    int    // native channel count (if discoverable)
}
```

Primary source: `ffmpeg -sources pulse`. For richer metadata (sample rate, channels), optionally parse `pactl list sources` output.

### Config persistence

All TUI operations write to config immediately via `config.SaveTo()`. The config is re-read on TUI launch to pick up any external edits.

## 5. Implementation Order

Suggested phasing (each phase is independently shippable):

### Phase 1: Config foundation
- Add `Devices`, `DeviceGroups` maps to config struct.
- Add `config.Save()` / `config.SaveTo()` for TOML round-tripping.
- Add device resolution logic: name -> check groups -> check aliases -> raw name -> "default".
- Tests for config load/save round-trip and device resolution.

### Phase 2: Diarization & transcription options
- Add `Diarize`, `SmartFormat`, `Punctuate` to `TranscribeOpts`.
- Add `Speaker` field to `Segment`.
- Add CLI flags `--diarize`, `--smart-format`, `--punctuate`.
- Add per-backend config fields (`deepgram.diarize`, etc.).
- Add validation in each backend's `Transcribe()`: error on unsupported options.
- Update Deepgram backend to pass `diarize`, `smart_format`, `punctuate` query params.
- Update whisperx backend to pass `--diarize` and `--hf_token` flags.
- Update output formatters to render `Speaker` prefix.
- Tests for validation errors, Deepgram query params, output formatting with speakers.

### Phase 3: Multi-device recording
- Extend `BuildFFmpegArgs` to accept `[]string` devices.
- Add `BuildFFmpegArgsMulti` with `filter_complex amix`.
- Update `cmd/record.go` to resolve groups before starting recorder.
- Update TUI `mic:` display for groups.
- Integration test: build ffmpeg args for 1, 2, 3 device groups.

### Phase 4: Device CLI subcommands
- Add `cmd/device.go` with `audiomemo device list|alias|group|default` subcommands.
- Non-interactive, writes to config.
- Tests for each subcommand.

### Phase 5: Device management TUI
- New bubbletea model in `internal/tui/devices.go`.
- Device browser with source/monitor split.
- Live VU preview (short-lived ffmpeg subprocess per selected device).
- Alias, group, default, delete workflows.
- Test recording with playback.
- Wire into `audiomemo device` (no args = TUI) and recording TUI `[d]` keybinding.
