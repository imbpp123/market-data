"""Regression checks for generated drift and Buf compatibility policy."""
import pathlib
import subprocess
import tempfile
import unittest

from google.protobuf import descriptor_pb2
import generate


class GenerationTest(unittest.TestCase):
    def test_two_generations_match_and_detect_missing_or_edited_files(self):
        with tempfile.TemporaryDirectory() as first, tempfile.TemporaryDirectory() as second:
            expected, actual = pathlib.Path(first), pathlib.Path(second)
            generate.generate(expected)
            generate.generate(actual)
            self.assertEqual([], generate.mismatches(expected, actual))
            missing, edited = generate.GENERATED[:2]
            (actual / missing).unlink()
            (actual / edited).write_text("changed generated file\n")
            self.assertEqual([missing, edited], generate.mismatches(expected, actual))

    def test_compatibility_fixtures(self):
        cases = ["optional addition", "type change", "number change", "removed method", "reserved number reuse", "reserved name reuse"]
        for case in cases:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as directory:
                base = descriptor_pb2.FileDescriptorSet.FromString(
                    (generate.ROOT / "api/compatibility/baseline.binpb").read_bytes())
                contract = next(file for file in base.file if file.package == "marketdata.v1")
                message = next(row for row in contract.message_type if row.name == "ErrorDetail")
                if case.startswith("reserved"):
                    message.reserved_range.add(start=2, end=3)
                    message.reserved_name.append("former_reason")
                candidate = descriptor_pb2.FileDescriptorSet()
                candidate.CopyFrom(base)
                updated = next(file for file in candidate.file if file.package == "marketdata.v1")
                error = next(row for row in updated.message_type if row.name == "ErrorDetail")
                if case == "optional addition":
                    error.oneof_decl.add(name="_extra")
                    error.field.add(name="extra", number=2, type=9, label=1, proto3_optional=True, oneof_index=0)
                elif case == "type change":
                    error.field[0].type = 3
                elif case == "number change":
                    error.field[0].number = 3
                elif case == "removed method":
                    del updated.service[0].method[0]
                else:
                    del error.reserved_range[:]
                    del error.reserved_name[:]
                    error.field.add(name="former_reason" if case == "reserved name reuse" else "extra",
                                    number=3 if case == "reserved name reuse" else 2, type=9, label=1)
                paths = [pathlib.Path(directory) / name for name in ("base.binpb", "new.binpb")]
                for path, value in zip(paths, [base, candidate]):
                    path.write_bytes(value.SerializeToString())
                result = subprocess.run([
                    str(generate.GO_TOOLS / "buf"), "breaking", str(paths[1]),
                    "--against", str(paths[0]), "--config", str(generate.ROOT / "api/compatibility/buf.yaml"),
                ], capture_output=True, text=True)
                if case == "optional addition":
                    self.assertEqual(0, result.returncode, result.stdout + result.stderr)
                else:
                    self.assertNotEqual(0, result.returncode, case)
                    self.assertTrue(result.stdout.strip(), result.stderr)


if __name__ == "__main__":
    unittest.main()
