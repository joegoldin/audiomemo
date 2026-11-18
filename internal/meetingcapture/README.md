# Meeting bundle v1

A meeting has a private `meetings/<id>/` directory. `manifest.json` records the
resolved endpoints and their roles, state, timeline offsets and gaps, and the
outcome of each derived job. `tracks/` contains original source-separated PCM
in numbered WAV segments (up to 30 seconds each, no classic WAV size limit for
the meeting as a whole). Regular FFmpeg devices are normalized at ingress to
48 kHz stereo signed 16-bit PCM. The native macOS tap keeps its sample rate and
channel count and converts float samples to signed 16-bit PCM. These are the
captured signals, not device-bit-perfect exports. The stereo mix resamples and
has fixed headroom; it switches to RF64 beyond classic WAV's size limit.

The first observed PCM anchors a source to the meeting's monotonic clock.
Subsequent frames use sample positions; at 30-second boundaries a median of
five recent sample-clock projections corrects independent device clocks when
the observed drift is within 5000 ppm. Derived audio fits the sample span to
successive anchors in the same uninterrupted epoch; a reopen starts a new
epoch so it cannot hide the outage by stretching the preceding audio. With
synthetic 8 and 16 kHz tracks, 400 ppm relative drift, delayed start and a
mid-meeting loss, aligned pulse onsets must remain within 20 ms over ten
minutes. Device/driver buffering, stalls and hardware latency can exceed that
budget on real hardware; the capture does not claim device-clock timestamps
or echo separation.

An interrupted capture can be retried after the capture lock is released.
Recovery ends at the last complete sample frame found on disk, not the time
of restart. A crash can leave the trailing WAV header advertising the segment
capacity; retry uses the actual complete frames and leaves the original alone.
Capture memory is bounded by per-source PCM blocks and a best-effort preview
queue, not meeting duration. Segment and manifest metadata grow with usage.
A stalled regular filesystem write cannot be interrupted portably; device
closure is independent from disk I/O, and Stop can time out without claiming
the manifest is durable. Each closed segment and successful publication is
synced before reporting success.

`mix.wav`, `transcript.json`, `transcript.txt`, optional SRT/VTT, and per-track
NeMo retry caches stay in the bundle. Retry never recaptures or overwrites
an existing edited artifact; move it aside if it should be regenerated.
Successful recognition windows are reused across a partial failure. Speaker
IDs are scoped to track and model window (up to ten minutes), never assumed
to identify the same person across them. Drafts are bounded, replaceable
snapshots; if draft ASR falls behind, finalization still starts from the
original tracks, not the preview.
