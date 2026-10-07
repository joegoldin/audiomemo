# Local diarization validation

Measured on Apple Silicon with NeMo-Speech.cpp 0.2.0, Metal, the cached
Nemotron 3 Diarization Q8 model, and the default `v3-offline` preset. No thresholds
were changed in the application. Audio was processed locally.

## Reference and provenance

The AMI ES2004a meeting has four human-annotated speakers. We evaluated both the
whole 1049.3546875-second meeting and a 120-second excerpt at source time 250–370s.
The excerpt's reference turns were clipped to that window and shifted to start
at zero. Overlapping speech was retained.

- [Source mixed-headset audio](https://groups.inf.ed.ac.uk/ami/AMICorpusMirror/amicorpus/ES2004a/audio/ES2004a.Mix-Headset.wav)
- [Pinned human-derived RTTM](https://raw.githubusercontent.com/pyannote/AMI-diarization-setup/67c2d539286e89f68952d5dcf83912bd9f01dfae/only_words/rttms/test/ES2004a.rttm)
- Audio SHA-256: `3e2560b19bee6952c7c7ce041b0f1ea8a7ea9468044c4eea79d2a2c67e24ab0f`
- RTTM SHA-256: `9869c6146c2fd9595403edb36c2caeda65c12ffa2c0af4ce48d6814b673fd5a9`

Attribution: AMI Meeting Corpus, ES2004a; J. Carletta et al., *The AMI Meeting
Corpus: A Pre-announcement* (2006). The RTTM was derived from AMI manual annotations
v1.6.2 by BUT Speech@FIT, distributed in the pyannote fork. This is the `only_words`
reference: non-word vocalizations are excluded. AMI is distributed under
[CC BY 4.0](https://groups.inf.ed.ac.uk/ami/download/).

## Scoring policy

`scripts/score_diarization.py` computes exact interval diarization error rate
(DER), with an optimal one-to-one mapping of anonymous speaker IDs across the
whole evaluated recording. It does not assume that model speaker 1 is reference
speaker 1 or provide the expected speaker count to the model.

DER is `(missed speaker time + false-alarm speaker time + confused speaker time)
/ reference speaker time`. Overlap contributes multiple reference speaker-seconds.
Silence is evaluated too; the supplied duration must include the trailing silence.
Duplicate/overlapping intervals for the same speaker count only once.

The primary results below use **no boundary collar and include overlapping
speech**. The optional `--collar 0.25` excludes 0.25 seconds on **each side** of
every reference start/end boundary, globally. That definition is explicit because
tools differ in collar processing; these scores should not be compared directly
to published numbers with a different policy.

This is a small dependency-free scorer, not NIST md-eval or pyannote.metrics.
Tests cover speaker permutations, merges, splits, overlaps, duplicate turns,
missed speech, false alarms, collars, malformed inputs, and matching the mapping
algorithm against exhaustive search. All models below use the same scorer.

## Results

Measured 2026-10-07. Percentages are fractions of reference speaker time, **not
word accuracy**. Lower is better.

| Audio / model | Speakers found | Miss | False alarm | Confusion | DER |
|---|---:|---:|---:|---:|---:|
| Whole meeting / NeMo V3 default | 4 | 14.58% | 5.85% | 0.92% | 21.36% |
| Whole meeting / FluidAudio offline default | 4 | 20.28% | 1.93% | 1.70% | 23.91% |
| Two-minute excerpt / NeMo V3 default | 4 | 26.82% | 5.99% | 2.00% | 34.81% |
| Two-minute excerpt / NeMo V3 onset/offset 0.3 | 4 | 20.04% | 10.77% | 2.47% | 33.28% |
| Two-minute excerpt / Sortformer V2 offline default | 4 | 28.69% | 8.01% | 2.17% | 38.87% |
| Two-minute excerpt / FluidAudio offline default | 2 | 29.71% | 3.59% | 11.11% | 44.41% |

With the optional symmetric 0.25s collar, default V3's whole-meeting DER is
13.68% (13.28% missed, 0.33% false alarm, 0.07% confusion); the excerpt's DER is
27.43%. These do not replace the strict scores above.

FluidAudio was built from commit `ea63ac36fcadb8a6611d0f58a8e4a0e7dd4381e2`.
Its built-in scorer uses a different policy and was not used for this table.
Full-attention V3, the streaming preset, and shorter minimum-turn settings did
not justify replacing the current final-pass configuration on the excerpt.

The rebuilt application's end-to-end results matched the standalone V3 speaker
turns. On the whole meeting it emitted 2218 ASR words, of which 27 were unassigned;
on the excerpt, 1 of 139 was unassigned. Those counts measure coverage only.
No word-error rate or human-verified word-to-speaker accuracy was established.

## Interpretation and implementation changes

The model separates the four reference identities well, especially with the
context of the complete meeting. Speech detection and boundaries remain weaker.
The excerpt is part of the same meeting, **not an independent held-out benchmark**;
these results do not establish general accuracy across microphones or speakers.
There is no evidence here for changing the backend or lowering default thresholds.

Two word-assignment defects were reproduced with failing tests and fixed:

- A speaker's fragmented turns now contribute their total covered time within
  a word, rather than competing separately against another speaker's single turn.
  Duplicate or overlapping turns do not inflate that time.
- Words whose start times are out of order no longer lose earlier speaker turns
  because the alignment cursor has advanced past them.

Unassigned speech is now explicit in diarized text/SRT/VTT. JSON keeps the speaker
field absent rather than inventing an identity. These changes do **not** change
raw model turns, so the turn-level DER above is unchanged by the fixes.

The short personal recording still fails: the default pipeline detects only one
speaker and leaves 18 of 20 recognized words unassigned, despite four voices in
the supplied human reference. Its transcription also misses speech. This is a
known sample-specific failure, not proof that the recording itself is defective.
No thresholds or speaker identities were forced to make it appear successful.

A separate CLI output-routing bug was found during validation: `--output` also
wrote the automatic sidecar beside the audio. Explicit output now replaces that
sidecar destination. Integration tests cover preservation of existing sidecars,
absence of unwanted new sidecars, and destination-write failures in all four
output formats.

## Reproduce locally

Download the public audio and pinned RTTM above to a scratch directory, retaining
their attribution. These commands assume those files and the NeMo models are
already available locally. Use explicit cached model paths in your configuration
if model downloads must also be prohibited.

```fish
# Explicit backend prevents cloud fallback; explicit output preserves other files.
audiomemo transcribe --backend nemo --diarize --format json \
    --output ami-hypothesis.json ES2004a.Mix-Headset.wav

python3 scripts/score_diarization.py ES2004a.rttm ami-hypothesis.json \
    --duration 1049.3546875

# Optional tolerance, reported separately from the strict result.
python3 scripts/score_diarization.py ES2004a.rttm ami-hypothesis.json \
    --duration 1049.3546875 --collar 0.25

python3 -m unittest discover -s scripts -p test_score_diarization.py
```

The scorer accepts audiomemo JSON, raw NeMo diarization JSON, or FluidAudio JSON.
It performs no inference, network access, downloads, or writes to input files.
For a combined RTTM, select a single meeting with `--recording-id`.

The local run's audio, references, raw predictions, comparison results, and
end-to-end outputs are retained under the gitignored
`.pi/tasks/diarization-reference-ami/` directory. Audio and private transcripts
are not added to the repository.
