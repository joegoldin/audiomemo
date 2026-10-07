#!/usr/bin/env python3
import itertools
import json
from pathlib import Path
import random
import tempfile
import unittest

from score_diarization import optimal_mapping, read_hypothesis, read_reference, score


class DiarizationScoreTests(unittest.TestCase):
    def test_permuted_labels_are_perfect(self):
        result = score([(0, 1, "a"), (1, 2, "b")], [(0, 1, "y"), (1, 2, "x")], 2)
        self.assertEqual(result["der_percent"], 0)
        self.assertEqual(result["mapping"], {"y": "a", "x": "b"})

    def test_missing_speech_is_not_good_diarization(self):
        result = score([(0, 2, "a")], [], 3)
        self.assertEqual(result["miss_percent"], 100)
        self.assertEqual(result["der_percent"], 100)

    def test_merging_and_splitting_speakers_are_errors(self):
        two = [(0, 1, "a"), (1, 2, "b")]
        one = [(0, 2, "x")]
        for reference, hypothesis in [(two, one), (one, two)]:
            result = score(reference, hypothesis, 2)
            self.assertEqual(result["confusion_percent"], 50)
            self.assertEqual(result["der_percent"], 50)

    def test_overlapping_speakers_count_separately(self):
        result = score([(0, 2, "a"), (0, 2, "b")], [(0, 2, "x")], 2)
        self.assertEqual(result["reference_speaker_seconds"], 4)
        self.assertEqual(result["miss_percent"], 50)

    def test_duplicate_turns_do_not_double_count(self):
        result = score([(0, 2, "a"), (1, 2, "a")], [(0, 2, "x"), (0, 2, "x")], 2)
        self.assertEqual(result["reference_speaker_seconds"], 2)
        self.assertEqual(result["der_percent"], 0)

    def test_false_alarm_in_trailing_silence(self):
        result = score([(0, 1, "a")], [(0, 2, "x")], 3)
        self.assertEqual(result["false_alarm_percent"], 100)

    def test_symmetric_collar_is_explicit_and_optional(self):
        reference, hypothesis = [(1, 2, "a")], [(0.8, 2.2, "x")]
        self.assertAlmostEqual(score(reference, hypothesis, 3)["der_percent"], 40)
        result = score(reference, hypothesis, 3, 0.25)
        self.assertEqual(result["der_percent"], 0)
        self.assertEqual(result["reference_speaker_seconds"], 0.5)

    def test_invalid_or_empty_evaluation_fails(self):
        for turns, duration, collar in [([], 2, 0), ([(0, 1, "a")], 0, 0),
                                       ([(0, 1, "a")], 2, float("nan")),
                                       ([(2, 1, "a")], 3, 0),
                                       ([(0, 0.1, "a")], 2, 1),
                                       ([(0, float("inf"), "a")], 2, 0)]:
            with self.assertRaises(ValueError):
                score(turns, [], duration, collar)

    def test_mapping_matches_exhaustive_search(self):
        rng = random.Random(0)
        for nr, nh in [(2, 4), (4, 2), (4, 4)]:
            rs, hs = [f"r{i}" for i in range(nr)], [f"h{i}" for i in range(nh)]
            for _ in range(20):
                weights = {(h, r): rng.randrange(10) for h in hs for r in rs}
                mapping = optimal_mapping(weights, rs, hs)
                actual = sum(weights[h, r] for h, r in mapping.items())
                expected = max(sum(weights[h, rs[i]] for h, i in zip(hs, permutation) if i < nr)
                               for permutation in itertools.permutations(range(max(nr, nh)), nh))
                self.assertEqual(actual, expected)

    def test_read_formats_and_multiple_recordings(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "hypothesis.json"
            for data in [{"speaker_turns": [{"start": 0, "end": 1, "speaker": "1"}]},
                         {"segments": [{"start": 0, "end": 1, "speaker": 1}]},
                         {"segments": [{"startTimeSeconds": 0, "endTimeSeconds": 1, "speakerId": "1"}]}]:
                path.write_text(json.dumps(data))
                self.assertEqual(read_hypothesis(path), [(0, 1, "1")])
            for malformed in ['{"segments":[{"start":0,"end":1}]}',
                              'null', '[]', '{"segments":{}}',
                              '{"segments":[null]}', 'not JSON']:
                path.write_text(malformed)
                with self.assertRaises(ValueError):
                    read_hypothesis(path)
            path = Path(directory) / "reference.rttm"
            path.write_text("SPEAKER one 1 0 1 <NA> <NA> a <NA> <NA>\n"
                            "SPEAKER two 1 0 1 <NA> <NA> b <NA> <NA>\n")
            with self.assertRaises(ValueError):
                read_reference(path)
            self.assertEqual(read_reference(path, "one"), [(0, 1, "a")])


if __name__ == "__main__":
    unittest.main()
