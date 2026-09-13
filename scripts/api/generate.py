"""Generate with pinned local tools; check mode never writes project files."""
import argparse
import importlib.metadata
import pathlib
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[2]
GO_TOOLS = ROOT / "bin/api-tools-go"
COPIED = {
    "api/go/CLIENT_GUIDE.md": "api/CLIENT_GUIDE.md",
    "api/python/src/marketdata/CLIENT_GUIDE.md": "api/CLIENT_GUIDE.md",
    "api/python/src/marketdata/v1/market_data.proto": "api/proto/marketdata/v1/market_data.proto",
}
GENERATED = (
    "api/go/marketdata/v1/market_data.pb.go",
    "api/go/marketdata/v1/market_data_grpc.pb.go",
    "api/python/src/marketdata/v1/market_data_pb2.py",
    "api/python/src/marketdata/v1/market_data_pb2.pyi",
    "api/python/src/marketdata/v1/market_data_pb2_grpc.py",
    "api/descriptor.binpb",
    *COPIED,
)


def generate(destination):
    import grpc_tools

    for name, version in {"grpcio-tools": "1.76.0", "protobuf": "6.33.5"}.items():
        if importlib.metadata.version(name) != version:
            raise RuntimeError(f"Unexpected {name} version; run make install-api-tools")
    for command, expected in (([sys.executable, "-m", "grpc_tools.protoc", "--version"], "libprotoc 31.1"),
                              ([str(GO_TOOLS / "protoc-gen-go"), "--version"], "protoc-gen-go v1.36.10"),
                              ([str(GO_TOOLS / "protoc-gen-go-grpc"), "--version"], "protoc-gen-go-grpc 1.5.1")):
        if subprocess.check_output(command, text=True).strip() != expected:
            raise RuntimeError(f"Unexpected generator version: {command[0]}")

    go = destination / "api/go"
    python = destination / "api/python/src"
    go.mkdir(parents=True, exist_ok=True)
    python.mkdir(parents=True, exist_ok=True)
    subprocess.run([
        sys.executable, "-m", "grpc_tools.protoc",
        "-I", str(ROOT / "api/proto"),
        "-I", str(pathlib.Path(grpc_tools.__file__).parent / "_proto"),
        "--plugin=protoc-gen-go=" + str(GO_TOOLS / "protoc-gen-go"),
        "--plugin=protoc-gen-go-grpc=" + str(GO_TOOLS / "protoc-gen-go-grpc"),
        "--go_out=" + str(go), "--go_opt=paths=source_relative",
        "--go-grpc_out=" + str(go), "--go-grpc_opt=paths=source_relative",
        "--python_out=" + str(python), "--pyi_out=" + str(python),
        "--grpc_python_out=" + str(python),
        "--descriptor_set_out=" + str(destination / "api/descriptor.binpb"),
        "--include_imports", "marketdata/v1/market_data.proto",
    ], check=True)
    for target, source in COPIED.items():
        path = destination / target
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes((ROOT / source).read_bytes())


def mismatches(expected, actual):
    return [name for name in GENERATED if not (actual / name).is_file()
            or (expected / name).read_bytes() != (actual / name).read_bytes()]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="market-data-generate-") as directory:
        destination = pathlib.Path(directory)
        generate(destination)
        if args.check:
            changed = mismatches(destination, ROOT)
            if changed:
                raise SystemExit("Generated output mismatch:\n" + "\n".join(changed))
        else:
            for name in GENERATED:
                target = ROOT / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes((destination / name).read_bytes())


if __name__ == "__main__":
    main()
