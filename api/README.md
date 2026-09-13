# Generated market data contract

The schema is `proto/marketdata/v1/market_data.proto`. The public service is `marketdata.v1.MarketDataService`, with four unary methods. Field numbers are now assigned. Required timestamps are application guarantees; Protobuf messages still track absence. Decimal values stay strings. No semantic validation is added to generated setters.

Go clients import `github.com/imbpp123/market-data/api/go/marketdata/v1`. The dedicated module has no exchange SDK dependency. Run its Go commands from `api/go`; the root module does not include it. The production service still uses HTTP. Phase 2 will consume the generated module through a local replacement.

Python clients install the project at `api/python`. See its [installation guide](python/README.md). The supported range is Python 3.13–3.14. Both boundary minor versions are checked with wheel and pinned local Git installs. This initial range is intentionally limited to verified runtimes. Exact dependency pins can conflict with another application's dependencies; change pins only with regeneration and compatibility/installation checks.

## Reproduce and check

Install Go 1.27.1 and Python 3.13 and 3.14. Put their versioned executable names on PATH. Then run from the repository root:

```sh
make generate-api
make check-api
make api-http-baseline
```

`API_PYTHON` selects the generator/bootstrap interpreter (default `python3.13`). `API_PYTHONS` lists package-test interpreters (default `python3.13 python3.14`). A missing interpreter fails the check. CI tests both supported versions before ordinary checks and before image publication. Downloads require access to Python and Go package sources; runtime tests use only loopback TCP and a temporary local Git repository.

Tool versions:

| Tool | Version |
| --- | --- |
| Go | 1.27.1 |
| protoc bundled in grpcio-tools | 31.1 |
| grpcio-tools / Python gRPC / grpcio-status | 1.76.0 |
| protoc-gen-go / Go Protobuf | 1.36.10 |
| protoc-gen-go-grpc | 1.5.1 |
| Go gRPC | 1.76.0 |
| Python Protobuf runtime | 6.33.5 |
| Buf | 1.59.0 |
| setuptools / wheel / build | 80.9.0 / 0.45.1 / 1.3.0 |

The bundled compiler emits Python code for Protobuf 6.31.1; the pinned 6.33.5 runtime accepts it. All development Python packages are pinned in `scripts/api/requirements.txt`. Python runtime dependencies are separate in `python/pyproject.toml`; Go runtimes are pinned in `go/go.mod` and `go/go.sum`. Generation verifies compiler/plugin versions and writes only managed generated files. Normal service builds need no generator or Python.

`descriptor.binpb` is generated. `compatibility/baseline.binpb` is the independent initial contract baseline. Do not replace it during generation. Buf FILE rules protect source and wire compatibility. Tests prove compatible optional additions and failures for changed type/number, removed methods, and reserved number/name reuse. A future baseline update needs a documented compatibility reason; it cannot silence a failure.

The schema and generated outputs, including the descriptor and `.pyi`, belong in source control. `make check-api` generates in a temporary directory, compares bytes, tests drift detection, checks Buf compatibility, runs nested Go build/vet/test/race, inspects a wheel, then tests isolated wheel/Git installations. Installation environments cannot find Go/protoc/grpcio-tools. The local Git commit exists only in a temporary fixture repository; the working repository is never committed or tagged by the check. The built wheel is also saved under ignored `bin/api-dist/`.

The [Go example](go/examples/client/main.go), [Python example](examples/client.py), and [async example](examples/client_async.py) reuse channels, set deadlines/receive limits and handle missing/unknown rich-status details. For local testing, run `go run ./cmd/contract-fixture` from `api/go`; it prints a loopback address. Pass that address to an example. The server is a fixed contract fixture with test selectors, not an application transport or a public API implementation.

The [phase 1 evidence](../docs/evidence/grpc-migration/README.md) records exact normal/full sizes and the existing HTTP TCP baseline. Full application behavior, send lifetime, overload and container capacity remain later migration gates.
