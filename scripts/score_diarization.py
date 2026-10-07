#!/usr/bin/env python3
"""Score local diarization JSON against RTTM without downloading audio or models."""

import argparse
import json
import math
from pathlib import Path


def optimal_mapping(weights, reference_speakers, hypothesis_speakers):
    # Put the smaller speaker set in the bitmask, not the recording's turns.
    transpose = len(reference_speakers) > len(hypothesis_speakers)
    rows, columns = (
        (reference_speakers, hypothesis_speakers)
        if transpose else (hypothesis_speakers, reference_speakers)
    )
    states = {0: (0.0, {})}
    for row in rows:
        updated = dict(states)
        for used, (total, mapping) in states.items():
            for i, column in enumerate(columns):
                if used & (1 << i):
                    continue
                h, r = (column, row) if transpose else (row, column)
                new_used = used | (1 << i)
                value = total + weights[h, r]
                if new_used not in updated or value > updated[new_used][0]:
                    updated[new_used] = (value, {**mapping, h: r})
        states = updated
    return max(states.values(), key=lambda state: state[0])[1]


def score(reference, hypothesis, duration, collar=0.0):
    """Exact interval DER; overlap included; collar is seconds on EACH side.

    Speaker identities are optimally mapped once over the whole recording.
    All time in [0, duration] is evaluated, including non-speech. Reference
    overlap contributes multiple speaker-seconds to the denominator.
    """
    if not math.isfinite(duration) or duration <= 0:
        raise ValueError("duration must be finite and positive")
    if not math.isfinite(collar) or collar < 0:
        raise ValueError("collar must be finite and nonnegative")
    boundaries = {0.0, duration}
    excluded = []
    for turns in (reference, hypothesis):
        for start, end, speaker in turns:
            if not (math.isfinite(start) and math.isfinite(end)) or start < 0 or end < start:
                raise ValueError("invalid speaker-turn timestamps")
            if not speaker:
                raise ValueError("missing speaker identity")
            boundaries.update((min(duration, start), min(duration, end)))
    if collar:
        for start, end, _ in reference:
            for point in (start, end):
                a, b = max(0, point - collar), min(duration, point + collar)
                if b > a:
                    excluded.append((a, b))
                    boundaries.update((a, b))
    rs = sorted({s for _, _, s in reference})
    hs = sorted({s for _, _, s in hypothesis})
    weights = {(h, r): 0.0 for h in hs for r in rs}
    intervals = []
    boundaries = sorted(boundaries)
    for a, b in zip(boundaries, boundaries[1:]):
        mid = (a + b) / 2
        if any(lo <= mid < hi for lo, hi in excluded):
            continue
        r = {s for lo, hi, s in reference if lo <= mid < hi}
        h = {s for lo, hi, s in hypothesis if lo <= mid < hi}
        dt = b - a
        for x in h:
            for y in r:
                weights[x, y] += dt
        intervals.append((dt, r, h))
    mapping = optimal_mapping(weights, rs, hs)
    total = miss = false_alarm = confusion = 0.0
    for dt, r, h in intervals:
        correct = sum(mapping.get(s) in r for s in h)
        total += dt * len(r)
        miss += dt * max(0, len(r) - len(h))
        false_alarm += dt * max(0, len(h) - len(r))
        confusion += dt * (min(len(r), len(h)) - correct)
    if total <= 0:
        raise ValueError("no reference speech remains in the evaluation region")
    return {
        "der_percent": 100 * (miss + false_alarm + confusion) / total,
        "miss_percent": 100 * miss / total,
        "false_alarm_percent": 100 * false_alarm / total,
        "confusion_percent": 100 * confusion / total,
        "reference_speaker_seconds": total,
        "mapping": mapping,
        "reference_speakers": len(rs),
        "hypothesis_speakers": len(hs),
        "collar_each_side_seconds": collar,
        "overlap": "included",
        "duration_seconds": duration,
    }


def read_reference(path, recording_id=None):
    recordings = {}
    for line in path.read_text().splitlines():
        fields = line.split()
        if not fields or fields[0].startswith("#"):
            continue
        if fields[0] != "SPEAKER":
            continue
        if len(fields) < 8:
            raise ValueError("malformed RTTM speaker row")
        start, duration = float(fields[3]), float(fields[4])
        recordings.setdefault(fields[1], []).append((start, start + duration, fields[7]))
    if recording_id is not None:
        if recording_id not in recordings:
            raise ValueError("recording ID not found in RTTM")
        return recordings[recording_id]
    if len(recordings) != 1:
        raise ValueError("RTTM must contain one recording, or use --recording-id")
    return next(iter(recordings.values()))


def read_hypothesis(path):
    try:
        data = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read hypothesis {path}: {error}") from error
    if not isinstance(data, dict):
        raise ValueError("hypothesis JSON must be an object")
    # Audiomemo, NeMo-Speech.cpp, and FluidAudio use these respective schemas.
    if "speaker_turns" in data:
        turns = data["speaker_turns"]
    elif "segments" in data:
        turns = data["segments"]
    else:
        raise ValueError("JSON has no speaker_turns or segments")
    if not isinstance(turns, list):
        raise ValueError("hypothesis turns must be an array")
    result = []
    for turn in turns:
        if not isinstance(turn, dict):
            raise ValueError("hypothesis turn must be an object")
        speaker = turn.get("speaker", turn.get("speakerId"))
        if speaker is None or speaker == "":
            raise ValueError("hypothesis turn is missing a speaker")
        start = turn.get("start", turn.get("startTimeSeconds"))
        end = turn.get("end", turn.get("endTimeSeconds"))
        if start is None or end is None:
            raise ValueError("hypothesis turn is missing timestamps")
        result.append((float(start), float(end), str(speaker)))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("reference", type=Path, help="human-reference RTTM")
    parser.add_argument("hypothesis", type=Path, help="diarization JSON")
    parser.add_argument("--duration", type=float, required=True, help="full audio duration, including silence")
    parser.add_argument("--recording-id", help="select one recording from a combined RTTM")
    parser.add_argument("--collar", type=float, default=0, help="excluded seconds on EACH side of every reference boundary")
    args = parser.parse_args()
    try:
        result = score(read_reference(args.reference, args.recording_id),
                       read_hypothesis(args.hypothesis), args.duration, args.collar)
    except (OSError, ValueError, TypeError) as error:
        parser.error(str(error))
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
