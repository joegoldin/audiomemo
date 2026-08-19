> **Disclaimer:** This software is provided "as is", without warranty of any
> kind. It is experimental, untested, non-production-ready code built with the
> assistance of LLMs (large language models). Use at your own risk. The
> author(s) accept no liability for any damage, data loss, or other issues
> arising from its use. See [LICENSE](LICENSE) for details.

# AUDIOTOOLS(1)

## NAME

audiomemo - record audio and transcribe it

## SYNOPSIS

    audiomemo record [flags]
    audiomemo transcribe [flags] <file>
    audiomemo device [command]

    record [flags]
    rect [flags]
    recw [flags]
    transcribe [flags] <file>

## DESCRIPTION

CLI for recording audio from PulseAudio/AVFoundation devices and
transcribing via local whisper or cloud APIs (ElevenLabs, Deepgram, OpenAI, Mistral).

The binary dispatches on `argv[0]`: symlinks named `record`, `rect`, `recw`,
or `transcribe` invoke those commands directly.

## COMMANDS

### record (alias: rect)

Record audio with a live TUI showing a streaming transcript. The cursor at
the end of the transcript doubles as a VU meter (height and color track the
mic level). Live transcription is always on when an ElevenLabs key is set.
When run without `-D`, an interactive device picker is shown first.

The TUI is drawn on the terminal even when stdout is redirected, so
`record | pbcopy` shows the interface and pipes the transcript.
See STDOUT AND PIPING.

    -D, --device string          input device name, alias, or group
    -d, --max-duration string    stop after this long (e.g. 30s, 5m, 1h30m)
        --max-silence string     stop after this much silence (e.g. 5s)
        --silence-threshold f    dBFS at or below which audio counts as
                                 silence (default -40)
        --print string           what to write to stdout: auto, path, text,
                                 both, none (default auto)
        --format string          output format: ogg, wav, flac, mp3
    -r, --sample-rate int        sample rate in Hz
    -c, --channels int           1=mono, 2=stereo
    -n, --name string            label for filename
        --temp                   save to temp directory
    -t, --transcribe             always run batch transcription on exit
                                 (as if quitting with Q)
        --no-live-transcription  disable live transcription while recording
        --transcribe-args string extra args passed to transcribe
    -v, --verbose                verbose output (passed to transcribe)
    -L, --list-devices           list devices and exit
        --no-tui                 headless mode
        --stream                 emit newline-delimited JSON on stdout
                                 (implies --no-tui; see STREAMING OUTPUT)
        --config string          config file path

TUI keybindings during recording:

    p, space    pause/resume
    q           stop, save, and keep the live transcript
    Q           stop, save, and batch-retranscribe (higher quality)
    ↑/↓         scroll transcript
    pgup/pgdn   page through transcript history
    end          jump to latest transcript

### recw

Record without live transcription, then batch-transcribe entirely locally.
`recw` prefers `whisper-cli` (whisper.cpp), falls back to the Python `whisper`
binary when whisper.cpp is unavailable, and fails rather than using a cloud
backend when neither local binary is installed. It accepts the same recording
flags and positional name as `record`.

### transcribe

Transcribe an audio file. Reads from stdin when file is `-`.
Auto-detects the best available backend if `--backend` is not set.

    -b, --backend string    elevenlabs, whisper, whisper-cpp, whisperx,
                            ffmpeg-whisper, deepgram, openai, mistral
    -m, --model string      model name (backend-specific)
    -l, --language string   language hint (ISO 639-1)
    -f, --format string     output format: text, json, srt, vtt (default: text)
    -o, --output string     output file (default: stdout)
    -v, --verbose           show progress and timing
    -C, --copy              copy output to clipboard
        --diarize           enable speaker diarization
        --smart-format      smart formatting (Deepgram)
        --punctuate         add punctuation (Deepgram)
        --store-in-cloud    keep transcript in cloud provider (default: false)
        --config string     config file path

### device

Manage audio devices. Run without a subcommand for the interactive TUI.

    device list                        list available devices
    device alias <name> <device>       create alias
    device group <name> <a1,a2,...>    create group from aliases
    device default <name>              set default recording device

## CONFIGURATION

TOML config at `$XDG_CONFIG_HOME/audiomemo/config.toml`
(default `~/.config/audiomemo/config.toml`).

On first run, an onboarding TUI prompts for initial device setup.

```toml
onboard_version = 1

[record]
format = "ogg"            # ogg, wav, flac, mp3
sample_rate = 48000
channels = 1
output_dir = "~/Recordings"
device = "mic"            # alias, group, or raw device name

[devices]
mic = "alsa_input.usb-Blue_Yeti-00.analog-stereo"
desktop = "alsa_output.pci-0000_0c_00.1.hdmi-stereo.monitor"

[device_groups]
zoom = ["mic", "desktop"]

[transcribe]
default_backend = "elevenlabs"
language = "en"
output_format = "text"

[transcribe.elevenlabs]
api_key = ""
api_key_file = "/run/agenix/elevenlabs_api_key"
model = "scribe_v2"
diarize = true
store_in_cloud = false        # delete transcript from cloud after fetching

[transcribe.whisper]
model = "base"
binary = "whisper"

[transcribe.deepgram]
api_key = ""
model = "nova-3"
diarize = false
smart_format = false
punctuate = false

[transcribe.openai]
api_key = ""
model = "gpt-4o-transcribe"

[transcribe.mistral]
api_key = ""
model = "voxtral-mini-latest"
```

## ENVIRONMENT

    ELEVENLABS_API_KEY       ElevenLabs API key (overrides config)
    ELEVENLABS_API_KEY_FILE  path to file containing ElevenLabs API key
    DEEPGRAM_API_KEY         Deepgram API key (overrides config)
    OPENAI_API_KEY           OpenAI API key (overrides config)
    MISTRAL_API_KEY          Mistral API key (overrides config)
    HF_TOKEN                 HuggingFace token for whisper model downloads

All `*_API_KEY` vars also support `*_API_KEY_FILE` variants that read
the key from a file at the given path (useful for secrets managers).

## DEVICE RESOLUTION

When resolving a device name (`-D` flag or `record.device` config):

1. Check `device_groups` - resolve each member alias, record all simultaneously
2. Check `devices` - return the mapped raw device name
3. Use as raw device name

Multi-device recording mixes all inputs via ffmpeg amix.

## LIVE TRANSCRIPTION

Whenever an ElevenLabs API key is configured, audio is streamed in realtime
to ElevenLabs for live speech-to-text unless `--no-live-transcription` is
passed. The transcript is the main content of the recording TUI; the cursor at
the insertion point doubles as a VU meter.

- Text appears as you talk (partial results in gray, committed text in white)
- Auto-scrolls to show latest text; scroll up to browse history
- `↓ live` indicator appears when scrolled up
- Live transcript is saved incrementally to `<name>-live.txt` (crash-safe)
- On quit, the live transcript is promoted to `<name>.txt`; quitting with `Q`
  (or passing `-t`) then overwrites it with the batch result
- If no ElevenLabs key is configured, recording shows a lone VU cursor and
  transcripts are only produced by `Q` / `-t` batch runs
- `recw` never starts live transcription and always runs a local Whisper batch
  transcription after recording

## STDOUT AND PIPING

`record` draws its interface on the terminal and keeps stdout for the thing
you asked for. Piping it is therefore safe: the TUI goes to `/dev/tty` (or
stderr if there is no controlling terminal), never into the pipe.

What lands on stdout is `--print`:

    auto   the default: the path when stdout is a terminal, the transcript
           when it is a pipe. Nobody writes `record | pbcopy` to put a
           filename on the clipboard.
    path   always the recording's path, for `transcribe $(record --print path)`
    text   always the transcript
    both   the path, then the transcript
    none   nothing

The transcript `text` emits is the batch result when a batch pass ran, and the
live transcript otherwise — the same file that ends up at `<name>.txt`. Asking
for `text` without live transcription running implies a batch pass, since it is
the only way to produce the words that were requested; speaker labels are
suppressed for it, as they are for `--stream`, because the text is headed
somewhere it will be read as prose. Pass `--transcribe-args "--diarize"` to keep
them.

`--print` cannot be combined with `--stream`, which fills stdout with NDJSON.
In `--clips` mode stdout stays a list of paths, one per clip, so only `path`
and `none` are accepted there.

Everything else — the status line, warnings, why a recording stopped — goes to
stderr, so a captured transcript stays clean.

## UNATTENDED RECORDING

`record` can run with no terminal and no keypress:

    record --no-tui -D mic --max-duration 2m --print text

`--max-duration` bounds the recording. `--max-silence` ends it once the room
has been quiet for that long, using `--silence-threshold` (default -40 dBFS)
to decide what counts as quiet. Either may fire; whichever comes first wins,
and stderr says which did.

The silence clock starts at the first sound above the threshold, not when
recording begins, so `--max-silence 2s` does not end the take while you are
still reaching for the mic. A recording that never rises above the threshold
therefore never trips it — pair the two flags when a run must terminate no
matter what.

Both work with the TUI too; the interface exits by itself when the recording
ends. With no terminal to draw on at all, `record` falls back to headless mode
and says so on stderr, and first-run setup is skipped rather than blocking.

## STREAMING OUTPUT

`record --stream` writes one JSON object per line to stdout while recording,
so another program can render the transcript and the mic level live.

    $ record --stream -D mic -t
    {"type":"start","t":0,"device":"alsa_input.usb-Blue_Yeti-00.analog-stereo","device_label":"mic","devices":["alsa_input.usb-Blue_Yeti-00.analog-stereo"],"path":"/home/joe/Recordings/recording-2026-08-18T14-30-05.ogg","format":"ogg","sample_rate":48000,"channels":1,"mode":"live","backend":"elevenlabs"}
    {"type":"level","t":52,"rms":0.21,"db":-47.4}
    {"type":"partial","t":1840,"text":"so the thing is"}
    {"type":"commit","t":2900,"text":"So the thing is,"}
    {"type":"final","t":9120,"text":"So the thing is, we shipped it.","path":"/home/joe/Recordings/recording-2026-08-18T14-30-05.ogg","transcript_path":"/home/joe/Recordings/recording-2026-08-18T14-30-05.txt","backend":"elevenlabs","source":"batch"}
    {"type":"end","t":9130,"reason":"signal","path":"/home/joe/Recordings/recording-2026-08-18T14-30-05.ogg","exit_code":0}

Every event carries `type` and `t` (milliseconds since the stream opened).

    start    once, after the pipeline is up. `mode` is `live` (partials will
             arrive), `batch` (no partials, one final after recording), or
             `none` (no transcript at all).
    level    `rms` on 0..1 and `db` in dBFS, coalesced to 20 Hz.
    partial  in-progress text; replaces the previous partial.
    commit   finalised text; append it.
    final    the finished transcript. `source` is `live` or `batch`.
    error    `scope` is record, stream, transcribe, or config; `fatal` says
             whether recording continued.
    end      always last. Reaching EOF without it means the producer died.

`--stream` implies `--no-tui`, suppresses the bare path line, and installs a
SIGINT/SIGTERM handler that stops ffmpeg gracefully and closes the stream with
`end{"reason":"signal"}`. A second signal exits immediately. It cannot be
combined with `--clips` or `--list-devices`.

Unknown event types must be skipped rather than treated as errors, so the
schema can grow.

## INSTALL

### Nix flake

```nix
# flake input
audiomemo.url = "github:joegoldin/audiomemo";

# overlay
audiomemo-packages = inputs.audiomemo.overlays.default;

# then add to packages
home.packages = [ pkgs.audiomemo ];
```

### Go

    go install github.com/joegoldin/audiomemo@latest

### Build from source

    nix build
    # or
    go build -o audiomemo .

## DEPENDENCIES

Runtime: `ffmpeg`. Optional: `whisper-cpp` (local transcription).

The nix package wraps the binary with ffmpeg and whisper-cpp in PATH.

## FILES

    ~/.config/audiomemo/config.toml    configuration
    ~/Recordings/                       default output directory

## EXAMPLES

    # Record with device picker, transcribe after
    record -t

    # Record privately, then transcribe locally with whisper.cpp or whisper
    recw private notes

    # Record specific device, 5 minute limit, headless
    record -D mic --max-duration 5m --no-tui

    # Dictate into the clipboard: TUI on the terminal, transcript down the pipe
    rect | pbcopy

    # Unattended: no terminal, no keypress, transcript on stdout
    record --no-tui -D mic --max-duration 2m --max-silence 5s --print text

    # Record group (multi-device), transcribe with ElevenLabs
    record -D zoom -t

    # Transcribe existing file
    transcribe recording.ogg

    # Transcribe with diarization, SRT output
    transcribe --diarize -f srt interview.wav

    # Transcribe with a specific backend
    transcribe -b deepgram -f srt interview.wav

    # Pipe audio from stdin
    cat audio.ogg | transcribe -

    # Manage devices interactively
    audiomemo device
