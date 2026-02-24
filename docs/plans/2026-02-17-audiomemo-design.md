# audiomemo - Audio Recording & Transcription CLI

## Overview

A Go project providing two CLI tools (`record`/`rec` and `transcribe`) built as a single binary with argv[0] dispatch (busybox-style). Includes a bubbletea TUI for recording with live VU meter and ambient animation. Packaged as a Nix flake with overlay for integration into the dotfiles repo.

## Decisions

- **Language**: Go
- **TUI**: charmbracelet/bubbletea + bubbles
- **Audio capture**: FFmpeg subprocess (cross-platform: `-f pulse` on Linux, `-f avfoundation` on macOS)
- **Output format**: OGG/Opus by default
- **Output location**: `~/Recordings/` by default, `--temp` for temp dir
- **File naming**: `recording-2026-02-17T14-30-05.ogg`, with optional `--name` label prefix
- **Config**: TOML at `$XDG_CONFIG_HOME/audiomemo/config.toml`
- **Binary dispatch**: Single binary, argv[0] detection → `rec`/`record` runs record, `transcribe` runs transcribe, `audiomemo` shows subcommands
- **Transcription auto-detect**: CLI flag → config default → first configured API key (deepgram, openai, mistral) → local whisper on PATH → error

## Project Structure

```
audiomemo/
├── flake.nix
├── flake.lock
├── go.mod                    # github.com/joe/audiomemo (or similar)
├── go.sum
├── main.go                   # argv[0] dispatch
├── cmd/
│   ├── record.go             # record CLI entry, flag parsing
│   └── transcribe.go         # transcribe CLI entry, flag parsing
├── internal/
│   ├── config/
│   │   └── config.go         # TOML loading, XDG resolution, env var merging
│   ├── record/
│   │   ├── recorder.go       # FFmpeg process management, level parsing
│   │   └── devices.go        # Device listing via ffmpeg
│   ├── transcribe/
│   │   ├── transcriber.go    # Transcriber interface, dispatcher
│   │   ├── whisper.go        # Local whisper backend (subprocess)
│   │   ├── deepgram.go       # Deepgram API client
│   │   ├── openai.go         # OpenAI API client
│   │   ├── mistral.go        # Mistral Voxtral API client
│   │   └── result.go         # Unified result types, format conversion
│   └── tui/
│       ├── model.go          # Main bubbletea model
│       ├── vu.go             # Vertical VU meter component (right side)
│       ├── animation.go      # Sine wave ambient animation (center)
│       └── devicepicker.go   # Device selection overlay
├── config.example.toml
└── README.md
```

## `transcribe` Command

### Usage

```
transcribe [flags] <file>
transcribe [flags] -          # read from stdin (buffered to temp file)
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--backend` | `-b` | auto | Force backend: `whisper`, `deepgram`, `openai`, `mistral` |
| `--model` | `-m` | per-backend | Model name (e.g. `large-v3`, `nova-3`, `gpt-4o-transcribe`, `voxtral-mini-latest`) |
| `--language` | `-l` | auto-detect | Language hint, ISO 639-1 code |
| `--output` | `-o` | stdout | Write transcription to file |
| `--format` | `-f` | `text` | Output format: `text`, `json`, `srt`, `vtt` |
| `--verbose` | `-v` | false | Show progress, backend info, timing |
| `--config` | | XDG default | Override config file path |

### Auto-detect Logic

1. `--backend` flag → use specified backend
2. Config `[transcribe] default_backend` → use it
3. Scan for API keys (env vars first, then config): deepgram → openai → mistral (first found wins)
4. Check if `whisper` or `whisper-cpp` is on `$PATH` → use local
5. Error with actionable message

### Backend Interface

```go
type Transcriber interface {
    Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error)
    Name() string
}

type TranscribeOpts struct {
    Model    string
    Language string
    Format   OutputFormat // text, json, srt, vtt
}

type Result struct {
    Text     string
    Segments []Segment // timestamps, speaker labels if available
    Language string
    Duration float64
}

type Segment struct {
    Start float64
    End   float64
    Text  string
}
```

### Backend: Local Whisper

Shells out to `whisper` or `whisper-cpp` CLI on PATH. No CGO, no go-whisper server dependency.

```
whisper --model <model> --language <lang> --output-format json <file>
```

Parse JSON output into `Result`. Default model: `base` (fast, reasonable quality).

The Nix flake can optionally include `whisper-cpp` as a runtime dependency if the user wants local transcription out of the box.

### Backend: Deepgram

**Endpoint**: `POST https://api.deepgram.com/v1/listen`

**Auth**: `Authorization: Token <DEEPGRAM_API_KEY>`

**Request**: Upload audio file as binary body with `Content-Type` matching the file format. Query params for options.

**Key query params used**:
- `model` - default `nova-3` (latest, best accuracy)
- `language` - BCP-47 tag, default `en`
- `smart_format=true` - punctuation, casing, formatting
- `punctuate=true` - add punctuation
- `detect_language=true` - when no language specified
- `utterances=true` - for SRT/VTT segment output

**Response parsing**: Extract `results.channels[0].alternatives[0].transcript` for plain text. Use `results.utterances[]` for timed segments (SRT/VTT output).

**Env var**: `DEEPGRAM_API_KEY`

### Backend: OpenAI

**Endpoint**: `POST https://api.openai.com/v1/audio/transcriptions`

**Auth**: `Authorization: Bearer <OPENAI_API_KEY>`

**Request**: `multipart/form-data` with fields:
- `file` - the audio file
- `model` - default `gpt-4o-transcribe`
- `language` - ISO 639-1 (optional)
- `response_format` - `json`, `text`, `srt`, `vtt`, `verbose_json`

**Response**: For `verbose_json`, returns `{ text, segments[{ start, end, text }], language, duration }`. For `text`, returns plain string.

**Env var**: `OPENAI_API_KEY`

### Backend: Mistral Voxtral

**Endpoint**: `POST https://api.mistral.ai/v1/audio/transcriptions`

**Auth**: `Authorization: Bearer <MISTRAL_API_KEY>`

**Request**: `multipart/form-data` with fields:
- `model` - default `voxtral-mini-latest` (alternatives: `voxtral-mini-2507`)
- `file` - the audio file (or `file_url` for URL-based)
- `language` - 2-char code (optional, boosts accuracy)
- `temperature` - optional
- `timestamp_granularities` - for segment-level timestamps

**Response**: `{ model, text, language, segments[{ start, end, text }], usage }`. Segments contain timed chunks.

**Env var**: `MISTRAL_API_KEY`

## `record` Command

### Usage

```
record [flags] [filename]
rec [flags] [filename]
```

If `filename` is provided, it's used as-is. Otherwise, auto-generated from timestamp + optional label.

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--duration` | `-d` | unlimited | Max recording duration (e.g. `5m`, `1h30m`) |
| `--format` | | `ogg` | Output format: `ogg`, `wav`, `flac`, `mp3` |
| `--device` | `-D` | system default | Input device name or index |
| `--list-devices` | `-L` | | List available input devices and exit |
| `--sample-rate` | `-r` | `48000` | Sample rate in Hz |
| `--channels` | `-c` | `1` | Channel count (1=mono, 2=stereo) |
| `--name` | `-n` | | Label prepended to timestamp in filename |
| `--temp` | | false | Save to OS temp directory instead of ~/Recordings |
| `--transcribe` | `-t` | false | After recording, pipe file path to `transcribe` |
| `--transcribe-args` | | | Extra args for transcribe (e.g. `--backend deepgram`) |
| `--no-tui` | | false | Headless mode, record until duration/signal |
| `--config` | | XDG default | Override config file path |

### TUI Layout

```
┌──────────────────────────────────────┐
│  ● REC  00:03:42       48kHz mono    │
│                                      │
│                                 ┃    │
│         ·    ·  · ·    ·        ┃    │
│        · ·  · ·· · ·  · ·     ████  │
│       ·   ··       ··   ·     ████  │
│        · ·  · ·· · ·  · ·     ████  │
│         ·    ·  · ·    ·       ████  │
│                                 ┃    │
│  mic: Built-in Microphone       ┃    │
│  out: meeting-2026-02-17T14.ogg ┃    │
│                                      │
│  [p]ause  [q]uit  [d]evices         │
└──────────────────────────────────────┘
```

**Vertical VU meter** (right side): Fills upward based on RMS level. Color gradient: green (bottom/low) → yellow (mid) → red (top/clipping). Driven by real-time audio level data from ffmpeg.

**Ambient animation** (center): A sine wave that oscillates back and forth on a tick timer. Not a waveform visualizer - more like a breathing/ripple animation that shows "we're live." Amplitude gently modulated by mic input level so it's livelier when speaking. Freezes in place when paused or stopped. Rendered with braille/dot unicode characters.

**Status line** (top): Red dot + "REC" when recording, "PAUSED" when paused, "SAVED" when done. Duration counter, sample rate, channel info.

**Info area** (bottom-left): Current mic device, output filename.

**Key bindings**:
- `p` / `space` - Pause/resume
- `q` / `Ctrl+C` - Graceful stop and save
- `d` - Open device picker overlay (bubbles list component)

### FFmpeg Integration

**Recording command** (Linux):
```
ffmpeg -f pulse -i <device> \
  -af astats=metadata=1:reset=1,ametadata=print:file=-  \
  -c:a libopus -b:a 64k -ar 48000 -ac 1 \
  <output.ogg>
```

**Recording command** (macOS):
```
ffmpeg -f avfoundation -i :<device_index> \
  -af astats=metadata=1:reset=1,ametadata=print:file=-  \
  -c:a libopus -b:a 64k -ar 48000 -ac 1 \
  <output.ogg>
```

**VU meter data**: The `astats` filter with `ametadata=print:file=-` prints audio statistics to stderr including `lavfi.astats.Overall.RMS_level`. Parse this in a goroutine reading ffmpeg's stderr, send level updates to the bubbletea model via a channel/command.

**Device listing**: `ffmpeg -sources pulse` (Linux) or `ffmpeg -sources avfoundation` (macOS). Parse output to extract device names and indices.

**Pause/resume**: Send `c` (toggle pause) to ffmpeg's stdin pipe.

**Graceful stop**: Send `q` to ffmpeg's stdin pipe, wait for process exit to ensure clean file closure.

### `--transcribe` Flow

1. Recording completes, file saved to disk
2. TUI shows "Transcribing..." status
3. Execute `transcribe <filepath>` as subprocess (with any `--transcribe-args`)
4. Stream transcribe stdout to TUI or terminal
5. Exit

## Config File

Located at `$XDG_CONFIG_HOME/audiomemo/config.toml` (typically `~/.config/audiomemo/config.toml`).

```toml
[record]
format = "ogg"
sample_rate = 48000
channels = 1
output_dir = "~/Recordings"
# device = "Built-in Microphone"

[transcribe]
# default_backend = "deepgram"
# language = "en"
# output_format = "text"

[transcribe.whisper]
# model = "base"
# binary = "whisper"       # or "whisper-cpp"

[transcribe.deepgram]
# api_key = ""             # or use DEEPGRAM_API_KEY env var
# model = "nova-3"

[transcribe.openai]
# api_key = ""             # or use OPENAI_API_KEY env var
# model = "gpt-4o-transcribe"

[transcribe.mistral]
# api_key = ""             # or use MISTRAL_API_KEY env var
# model = "voxtral-mini-latest"
```

### Resolution Order (per setting)

1. CLI flag (highest priority)
2. Environment variable (`DEEPGRAM_API_KEY`, `OPENAI_API_KEY`, `MISTRAL_API_KEY`)
3. Config file value
4. Built-in default

Config file missing entirely is fine - all defaults work without it.

## Nix Flake

```nix
{
  description = "Audio recording and transcription CLI tools";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        audiomemo = pkgs.buildGoModule {
          pname = "audiomemo";
          version = "0.1.0";
          src = ./.;
          vendorHash = ""; # update after go mod tidy
          nativeBuildInputs = [ pkgs.makeWrapper ];
          postInstall = ''
            ln -s $out/bin/audiomemo $out/bin/record
            ln -s $out/bin/audiomemo $out/bin/rec
            ln -s $out/bin/audiomemo $out/bin/transcribe
            wrapProgram $out/bin/audiomemo \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.ffmpeg ]}
          '';
        };
      in {
        packages.default = audiomemo;
        packages.audiomemo = audiomemo;
        devShells.default = pkgs.mkShell {
          buildInputs = [ pkgs.go pkgs.gopls pkgs.ffmpeg ];
        };
      }
    ) // {
      overlays.default = final: prev: {
        audiomemo = self.packages.${final.system}.audiomemo;
      };
    };
}
```

### Dotfiles Integration

In `flake.nix`:
```nix
inputs.audiomemo.url = "github:joe/audiomemo";
```

In overlays:
```nix
audiomemo-packages = inputs.audiomemo.overlays.default;
```

In home packages or system packages:
```nix
pkgs.audiomemo
```

Config via home-manager:
```nix
xdg.configFile."audiomemo/config.toml".source = ./audiomemo.toml;
```

Or generate from Nix options / wire API keys from agenix secrets.

## Error Handling

### record
- **FFmpeg not found**: "ffmpeg not found on PATH. Install it or run via nix."
- **No audio devices**: "No input devices found. Check your audio setup."
- **Device disconnects mid-recording**: Save what we have, show "Recording saved (device disconnected)."
- **Disk full**: Catch ffmpeg error, report bytes written.
- **`--transcribe` with no backend**: Recording succeeds and saves; transcribe step fails with auto-detect error.
- **Ctrl+C**: Graceful shutdown - send `q` to ffmpeg stdin, wait for clean file close.

### transcribe
- **File not found**: Clear error with path shown.
- **Unsupported format**: Auto-convert to WAV via ffmpeg temp file, transparent to user.
- **API auth failure**: "Authentication failed for <backend>. Check your API key."
- **Network error**: Retry once with backoff, then fail suggesting local whisper.
- **Whisper not on PATH**: "whisper not found. Install whisper-cpp or configure a remote backend."
- **Stdin mode**: Buffer to temp file (APIs need content-length/seekable files).

### General
- **Config missing**: Fine, all defaults work. No warning.
- **Config malformed**: Error with line number from TOML parser.
- **Unknown flags**: Standard cobra/pflag error with `--help` hint.

## Go Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/charmbracelet/bubbletea` | TUI framework |
| `github.com/charmbracelet/bubbles` | TUI components (list, spinner, etc.) |
| `github.com/charmbracelet/lipgloss` | TUI styling and layout |
| `github.com/spf13/cobra` | CLI framework, subcommands, flag parsing |
| `github.com/pelletier/go-toml/v2` | TOML config parsing |
