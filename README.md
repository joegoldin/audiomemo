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
transcribing locally with NeMo-Speech.cpp, with ElevenLabs fallback when configured.
Whisper, Deepgram, OpenAI, and Mistral remain available as explicit backends.

The binary dispatches on `argv[0]`: symlinks named `record`, `rect`, `recw`,
or `transcribe` invoke those commands directly.

## COMMANDS

### record (alias: rect)

Record audio with a live TUI showing a streaming transcript. The cursor at
the end of the transcript doubles as a VU meter (height and color track the
mic level). Live preview defaults to local NeMo, unless disabled with
`--no-live-transcription`. Explicit backends and configured fallback are honored.
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
Defaults to local NeMo, with ElevenLabs fallback only if a key is configured.
Use `--backend nemo` for local-only or `--backend elevenlabs` for cloud-only.

    -b, --backend string    auto, nemo, elevenlabs, whisper, whisper-cpp, whisperx,
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
default_backend = "auto"       # local first; "nemo" means local-only
language = "en"
output_format = "text"

[transcribe.nemo]
binary = "nemo-speech"
model = "parakeet-tdt"
live_model = "nemotron-en"      # English; nemotron-3.5 for multilingual preview
diar_model = "nemotron-3-diarization"
device = "auto"                # cpu, metal, vulkan:0, cuda:0
diarize = true                 # up to eight speakers
startup_timeout = 120          # seconds to start the live server

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

### macOS system audio

On macOS 14.2 or later, **System audio** appears under MONITORS in the
picker. It uses a native Core Audio process tap: no BlackHole, Loopback,
SoundSource, or other audio driver is required. Playback continues normally.
The tap captures the system-wide output mix, not an individual speaker device.

Select your microphone and System audio with Space, then press Enter to record
both. The saved recording and live transcription receive the combined mix.
Use headphones to avoid also picking up speaker playback through the microphone.

To record system audio alone:

```sh
rect -D system-audio
```

For a reusable microphone-plus-system group, add aliases using the microphone
name shown by `audiomemo device list`:

```toml
[devices]
mic = "MacBook Pro Microphone"
desktop = "system-audio"

[device_groups]
meeting = ["mic", "desktop"]
```

Then run `rect -D meeting`. This config is an example; merge it into your
existing tables rather than adding duplicate TOML tables.

macOS may ask for system-audio-recording permission. If access is denied or the
capture stays silent while audio is playing, check **System Settings > Privacy
& Security > Screen & System Audio Recording** for Audiomemo or the terminal
hosting it, then restart the command. Microphone access is a separate permission.

Native capture is included in the macOS Nix build. Building from source requires
cgo enabled and a macOS SDK with the Core Audio tap APIs (14.2 or newer).
Builds without cgo and older macOS versions retain microphone recording but do
not offer System audio. Pause/mute is currently PulseAudio-only; do not rely on
it to silence a macOS recording.

## LIVE TRANSCRIPTION

`record` starts a private loopback NeMo server for **Nemotron English 0.6B**
live preview. The TUI shows partial and committed text while recording. Startup
runs in the background; preview failure or lag does not block the saved audio.
Preview buffering is bounded, so a slow startup can drop preview audio without
dropping it from the recording. Pre-download models before recording.

In default **auto** mode, a local startup or inference failure switches live
preview to ElevenLabs if a key is configured. The UI reports the switch; audio
already consumed by the failed stream is not replayed. `--backend nemo` prohibits
cloud fallback, including live preview. Explicit non-streaming backends get a
final batch pass only. The same selection is respected through
`record --transcribe-args="--backend nemo"`.

After recording, the live server is stopped and its model unloaded. When final
transcription is requested (`-t` or `Q`), a fresh
**Parakeet TDT v3** pass transcribes the original recording, followed by a separate
**Nemotron 3 Diarization** process using the long-recording `v3-offline` chunked
preset, not the short-recording full-attention mode. Word timestamps are aligned
with speaker activity by maximum overlap. JSON preserves both words and original
speaker turns (including overlapping activity); text and subtitles use one speaker
per word. This does not separate simultaneous voices or identify people by name.

- Live text is saved incrementally to `<recording>-live.txt`.
- The final transcript is saved alongside the audio (`.txt`, `.json`, `.srt`, or `.vtt`).
- `q` preserves the preview; `Q` or `-t` also runs the final pass.
- `--no-live-transcription` disables preview; combine with `-t` for batch-only.
- `recw` remains local Whisper-only, without live preview.
- The upstream transcript-first TUI, VU cursor, and scrolling are unchanged.
- Successful preview **never** skips a requested final pass.
- Final local failure retries the original recording through ElevenLabs only in
  auto mode and only with a configured key. Failed/canceled final processing
  leaves the preview file intact. Cancellation does not trigger a cloud upload.
- Other cloud providers and Whisper require explicit selection; they are not
  silently used as additional fallbacks.
- Existing configs with `default_backend = "elevenlabs"` remain cloud-only.
  Change that setting to `"auto"` to opt into the new default behavior.

Models are loaded sequentially to reduce peak memory. There is no enforced 8 GB
memory cap, and hardware-specific speed and long-recording memory usage need to
be measured on the target machine.

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
The start backend is the initial preview selection; fallback notices arrive as
nonfatal stream errors. A batch backend of `auto` denotes the local-first policy.

    start    once, after the pipeline is up. `mode` is `live` (preview
             enabled; local models may still be loading), `batch` (no partials, one final after recording), or
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

Runtime: `ffmpeg` and **NeMo-Speech.cpp 0.2.0 or newer**, built with ASR,
diarization, and HTTP/WebSocket support. Optional: `whisper-cpp` for the explicit
Whisper backend. The Nix package supplies ffmpeg, whisper-cpp, NeMo-Speech.cpp
0.2.0, and the three default Q8 models for live ASR, final ASR, and diarization.
It uses the pinned upstream Metal release on Apple Silicon, CPU on Intel macOS,
and Vulkan (with CPU support) on x86_64 and ARM64 Linux. Linux GPU use requires
working Vulkan drivers on the host.

Nix downloads the models at build time (about 1.5 GB). The runtime wrapper seeds
missing entries in NeMo's writable model cache with links to the Nix store;
existing cached files and `NEMO_SPEECH_MODEL_DIR` are respected. The default
models therefore need no first-recording download. Other model selections still
use NeMo's normal downloader. Model licenses remain separate from the runtime.

`nix build .#nemo-speech` builds the runtime and models separately; the normal
`audiomemo` package includes them on its private PATH. Home Manager users should
set `programs.audiomemo.settings.transcribe.default_backend = "auto"` for local
first with configured ElevenLabs fallback, or `"nemo"` for local-only. An explicit
`"elevenlabs"` setting still bypasses local inference.

### Local model setup (macOS / Linux)

The following manual setup is only needed outside Nix.
Use the [official NeMo-Speech.cpp installer](https://github.com/NVIDIA/NeMo-Speech.cpp/blob/main/docs/install.md).
Inspect the installer before running it. Select **Metal** on Apple Silicon,
**Vulkan** on Linux with an AMD GPU, or **CPU** on either platform. The upstream
Linux installer defaults to CPU when no NVIDIA GPU is detected, so select Vulkan
explicitly for AMD. Native source builds are also supported upstream.

```sh
# After installing the appropriate nemo-speech build:
nemo-speech doctor
nemo-speech pull nemotron-en
nemo-speech pull parakeet-tdt
nemo-speech pull nemotron-3-diarization

# Verify local-only transcription; no cloud fallback:
audiomemo transcribe --backend nemo --format json recording.ogg
```

These indexed names select Q8 model artifacts (about 1.5 GB total downloads).
NeMo verifies downloads against its model index. Missing indexed models download
on first use; explicit local GGUF paths and cached models allow offline operation.
Set `NEMO_SPEECH_MODEL_DIR` to choose the cache directory. For an offline-only
workflow, also use `--backend nemo` so an available ElevenLabs key cannot cause
an upload after a local failure.

For Linux/AMD, set `transcribe.nemo.device = "vulkan:0"`; for Apple Silicon use
`"metal"`, or leave `"auto"` for runtime selection. Use `"cpu"` when the GPU
backend is unavailable. No CUDA or Python dependency is required by audiomemo's
NeMo integration. Model licenses are separate from the runtime: Parakeet v3 is
CC-BY-4.0, English Nemotron uses the NVIDIA Open Model License, and Nemotron 3
Diarization uses OpenMDW 1.1.

### Native integration check

The default tests use fake subprocesses and local WebSocket servers, not cloud
APIs or model downloads. With the runtime and all three models already cached:

```sh
AUDIOMEMO_NEMO_TEST_BINARY=/path/to/nemo-speech \
  go test ./internal/transcribe -run TestNemoNative -v
```

The opt-in check uses only the repository's `testdata/test.ogg`.

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
