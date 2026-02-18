> **Disclaimer:** This software is provided "as is", without warranty of any
> kind. It is experimental, untested, non-production-ready code built with the
> assistance of LLMs (large language models). Use at your own risk. The
> author(s) accept no liability for any damage, data loss, or other issues
> arising from its use. See [LICENSE](LICENSE) for details.

# AUDIOTOOLS(1)

## NAME

audiotools - record audio and transcribe it

## SYNOPSIS

    audiotools record [flags]
    audiotools transcribe [flags] <file>
    audiotools device [command]

    record [flags]
    rec [flags]
    transcribe [flags] <file>

## DESCRIPTION

CLI for recording audio from PulseAudio/AVFoundation devices and
transcribing via local whisper or cloud APIs (Deepgram, OpenAI, Mistral).

The binary dispatches on `argv[0]`: symlinks named `record`, `rec`, or
`transcribe` invoke those subcommands directly.

## COMMANDS

### record (alias: rec)

Record audio with a live TUI showing a scrolling waveform and VU meter.
When run without `-D`, an interactive device picker is shown first.

    -D, --device string          input device name, alias, or group
    -d, --duration string        max duration (e.g. 5m, 1h30m)
        --format string          output format: ogg, wav, flac, mp3
    -r, --sample-rate int        sample rate in Hz
    -c, --channels int           1=mono, 2=stereo
    -n, --name string            label for filename
        --temp                   save to temp directory
    -t, --transcribe             transcribe after recording
        --transcribe-args string extra args passed to transcribe
    -v, --verbose                verbose output (passed to transcribe)
    -L, --list-devices           list devices and exit
        --no-tui                 headless mode (Ctrl+C to stop)
        --config string          config file path

TUI keybindings during recording:

    p, space    pause/resume
    q           stop and save
    Q           stop, save, and transcribe

### transcribe

Transcribe an audio file. Reads from stdin when file is `-`.
Auto-detects the best available backend if `--backend` is not set.

    -b, --backend string    whisper, whisper-cpp, whisperx, ffmpeg-whisper,
                            deepgram, openai, mistral
    -m, --model string      model name (backend-specific)
    -l, --language string   language hint (ISO 639-1)
    -f, --format string     output format: text, json, srt, vtt (default: text)
    -o, --output string     output file (default: stdout)
    -v, --verbose           show progress and timing
    -C, --copy              copy output to clipboard
        --diarize           enable speaker diarization
        --smart-format      smart formatting (Deepgram)
        --punctuate         add punctuation (Deepgram)
        --config string     config file path

### device

Manage audio devices. Run without a subcommand for the interactive TUI.

    device list                        list available devices
    device alias <name> <device>       create alias
    device group <name> <a1,a2,...>    create group from aliases
    device default <name>              set default recording device

## CONFIGURATION

TOML config at `$XDG_CONFIG_HOME/audiotools/config.toml`
(default `~/.config/audiotools/config.toml`).

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
default_backend = "deepgram"
language = "en"
output_format = "text"

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

    DEEPGRAM_API_KEY    Deepgram API key (overrides config)
    OPENAI_API_KEY      OpenAI API key (overrides config)
    MISTRAL_API_KEY     Mistral API key (overrides config)
    HF_TOKEN            HuggingFace token for whisper model downloads

## DEVICE RESOLUTION

When resolving a device name (`-D` flag or `record.device` config):

1. Check `device_groups` - resolve each member alias, record all simultaneously
2. Check `devices` - return the mapped raw device name
3. Use as raw device name

Multi-device recording mixes all inputs via ffmpeg amix.

## INSTALL

### Nix flake

```nix
# flake input
audiotools.url = "github:joegoldin/audiotools";

# overlay
audiotools-packages = inputs.audiotools.overlays.default;

# then add to packages
home.packages = [ pkgs.audiotools ];
```

### Go

    go install github.com/joegoldin/audiotools@latest

### Build from source

    nix build
    # or
    go build -o audiotools .

## DEPENDENCIES

Runtime: `ffmpeg`. Optional: `whisper-cpp` (local transcription).

The nix package wraps the binary with ffmpeg and whisper-cpp in PATH.

## FILES

    ~/.config/audiotools/config.toml    configuration
    ~/Recordings/                       default output directory

## EXAMPLES

    # Record with device picker, transcribe after
    record -t

    # Record specific device, 5 minute limit, headless
    record -D mic -d 5m --no-tui

    # Record group (multi-device), transcribe with Deepgram
    record -D zoom -t --transcribe-args="--backend deepgram"

    # Transcribe existing file
    transcribe recording.ogg

    # Transcribe with diarization, SRT output
    transcribe -b deepgram --diarize -f srt interview.wav

    # Pipe audio from stdin
    cat audio.ogg | transcribe -

    # Manage devices interactively
    audiotools device
