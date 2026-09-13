"""Test comparison accounting without network or timing assertions."""
import unittest

from compare_transports import connection_delta, summarize


class ComparisonTest(unittest.TestCase):
    def test_explicit_percentiles_and_raw_samples(self):
        result = summarize({"latency_ns": list(reversed(range(100))), "before": {"cpu_seconds": 2}})
        self.assertEqual(100, result["samples"])
        self.assertEqual({"p50": 50, "p95": 95, "p99": 99}, result["latency_ns"])
        self.assertEqual(list(range(100)), result["raw_latency_ns"])
        self.assertEqual({"cpu_seconds": 2}, result["before"])

    def test_small_sample_tail_and_invalid_input(self):
        self.assertEqual({"p50": 7, "p95": 7, "p99": 7}, summarize({"latency_ns": [7]})["latency_ns"])
        for values in ([], [-1]):
            with self.subTest(values=values), self.assertRaises(ValueError):
                summarize({"latency_ns": values})

    def test_connection_counts_include_frames_not_only_messages(self):
        before = {"connection_read_bytes": 100, "connection_written_bytes": 200}
        after = {"connection_read_bytes": 170, "connection_written_bytes": 1500}
        self.assertEqual({"connection_read_bytes": 70, "connection_written_bytes": 1300}, connection_delta(before, after, 1000))
        with self.assertRaises(ValueError):
            connection_delta(after, before, 0)
        with self.assertRaises(ValueError):
            connection_delta(before, after, 1400)


if __name__ == "__main__":
    unittest.main()
