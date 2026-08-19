# `record --stream` Design

Date: 2026-08-18
Status: Approved

## Goal

Give `record` a machine-readable stdout channel so a program can render the
live transcript and the mic level while recording, without scraping the TUI.
One JSON object per line, written as it happens.

## Why a flag

`--stream` composes with `-D/--device`, `-d/--duration`, `--format`, `-r`, `-c`,
`-n`, `--temp`, `-t`, and `--no-live-transcription`. A `recstream` symlink or a
`record stream` subcommand would have to redeclare all of them, and the argv[0]
dispatch in `main.go` is already carrying four names.

## Event types

    start    once, after the pipeline is up. Carries device, path, and mode.
    level    the mic RMS, coalesced to 20 Hz.
    partial  in-progress text from the realtime backend; replaces the previous.
    commit   text the realtime backend finalised; appended.
    final    the finished transcript, from the live session or the batch pass.
    error    something failed; `fatal` says whether recording continued.
    end      always last. `reason` says why, `exit_code` says how it went.

`start.mode` is `live` when partials will arrive, `batch` when no partials will
arrive but a batch pass will produce one `final`, and `none` when there will be
no transcript at all. It is emitted after the streamer has connected, so it is
a statement of fact rather than an intention.

## What stdout carries

Under `--stream`, the NDJSON is the whole of stdout. The bare path line
`record` prints today would break a line-oriented consumer's parser, so it is
suppressed; `path` appears on `start`, `final`, and `end` instead. The batch
`transcribe` subprocess normally inherits stdout, so under `--stream` its
stdout is captured into a buffer and delivered as `final.text`.

Scripts that do `transcribe $(record)` must not pass `--stream`. That is the
whole of the compatibility story: the flag opts into a different contract.

## Termination

`--stream` implies `--no-tui`: bubbletea's alternate screen and an NDJSON
consumer cannot both own stdout. It also installs a SIGINT and SIGTERM handler,
which `--no-tui` does not have today. On the first signal, ffmpeg is stopped
the graceful way (`Recorder.Stop` writes `q` to its stdin), the transcript is
promoted, the batch pass runs if it was asked for, and the stream closes with
`end{reason:"signal", exit_code:0}`. A deliberate stop is not a failure. The
signal is then restored to its default disposition, so a second Ctrl-C kills
the process outright rather than hanging on a wedged ffmpeg.

A consumer that reaches EOF without having seen `end` knows the producer died.

## Rejected combinations

`--stream --clips` and `--stream --list-devices` both fail with an error.
Clips mode is an interactive TUI loop; `--list-devices` prints a human table.
Both want stdout for something that is not NDJSON. Use `audiomemo device list`
for enumeration.

## Levels

ffmpeg's `astats` filter prints one RMS line per 480 samples, which is 100 a
second at 48 kHz. That is more than any consumer needs, so the emitter keeps
the loudest reading per 50 ms window and emits at 20 Hz. Smoothing stays with
the consumer: the wire carries measurements, not a rendering.

`astats` reports digital silence as `-inf` and can report `inf`. `encoding/json`
refuses to marshal either, and a refused encode drops the whole line, so `db`
is clamped to [-60, 0] and `rms` is the same reading normalised onto [0, 1]
against the same -60 dBFS floor the TUI already uses.

## Errors

Everything audiomemo would have written to stderr as a warning becomes an
`error` event with a `scope` (`record`, `stream`, `transcribe`, `config`) and a
`fatal` flag. Subprocess stderr still goes to fd 2, because ffmpeg's and
whisper's diagnostics are not audiomemo's to reformat.

## Out of scope

Device enumeration over the stream, pause/resume control on stdin, and any
change to the batch backends or output formats.
