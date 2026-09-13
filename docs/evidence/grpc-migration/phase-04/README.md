# Phase 4 evidence

Implementation checks, measurements and independent final review passed. The [verification report](../../../grpc-migration-verification.md) owns conclusions and limitations. No historical phase 1/2/3 evidence was changed.

## Required gates

| Command | Evidence | Result |
| --- | --- | --- |
| `make check` | [check.log](check.log) | PASS: formatting, build/config, lint, unit and race tests |
| `make vet` | [vet.log](vet.log) | PASS |
| `make check-api` | [check-api-review.log](check-api-review.log) | PASS: generation, compatibility, nested Go and installed Python 3.13/3.14 wheel/Git consumers |
| `make docker-build` | [docker-build.log](docker-build.log) | PASS |
| `make docker-verify` | [docker-verify.log](docker-verify.log) | PASS: internal network, both protocols, hardening, limits and shutdown |
| `make release-load` | [release-load.log](release-load.log) | PASS: four TCP channels and the unchanged main profile |
| Linux main capacity | [capacity.log](capacity.log) | PASS: peak process RSS 775,417,856 bytes |
| Real TCP matrix | [comparison.log](comparison.log), [comparison-linux.log](comparison-linux.log) | PASS: 108 cases per environment |
| Repeated native overload/slow readers | [overload-slow-readers.log](overload-slow-readers.log) | PASS: 100 cases; 59,752,448-byte process peak RSS |
| Enabled operational routes under saturation | [operations-saturation.log](operations-saturation.log) | PASS: 20 repetitions |

[checks.json](checks.json) records source and binary identity. Production service logic and generated schema did not change in phase 4. After the full root gate, changes were limited to the measurement benchmark, Linux harness options, contract-fixture error selectors, atomic publication of the installed-composition manifest and documentation. The affected benchmark, comparison unit tests, formatting/lint and final API gate passed. These later test/tooling changes do not alter the measured server, capacity loop or image executable. No broad successful gate was repeated without a relevant change.

The final Docker/capacity/comparison containers' enforced settings and exit/OOM states are in [linux-containers.json](linux-containers.json). [images.json](images.json), [docker-version.json](docker-version.json) and [hardware.log](hardware.log) identify the runtime. Container limits are not claimed as measured peak usage. The capacity probe inherits a service healthcheck without a config mount and is therefore marked unhealthy; its three missing-config healthcheck failures remain in the raw state. This is separate from the healthy configured service checked by `docker-verify`. The commands below show the measured setup; future capacity probes can add `--no-healthcheck` to omit that unrelated check.

## Reproduction

Run from the repository root at the source state in the manifest. Local Go caches, TCP listeners and Docker need the corresponding local permissions. Install the pinned tooling with the existing Makefile. No exchange credentials are needed.

The HTTP baseline is an isolated clone of base `1965b0ab10b3d8cfdb444b0f7d6b8a10e4eb175a`. Apply only [baseline-instrumentation.patch](baseline-instrumentation.patch) there; it adds a GC command between measured cases. Its archived handlers and domain fixture source stay byte-identical. Build its `./scripts/api/httpfixture` into a host binary and a `CGO_ENABLED=0 GOOS=linux` binary. The original clone used `/private/tmp/market-data-phase04-baseline`.

```sh
go build -o bin/api-comparison ./scripts/api/httpfixture
CGO_ENABLED=0 GOOS=linux go build -o bin/phase04-linux-grpc ./scripts/api/httpfixture
python3.13 -m venv bin/phase04-client
bin/phase04-client/bin/python -m pip install bin/api-dist/market_data_api-0.1.0-py3-none-any.whl
bin/phase04-client/bin/python scripts/api/compare_transports.py \
  --baseline /private/tmp/market-data-phase04-http-baseline \
  --output /tmp/phase04-host-results
```

The driver exports fixed Protobuf bodies under `bin/phase04-fixtures`, checks semantic identity, and records all raw latency samples and separate connection/message bytes. It defaults to three warmups and 20 samples per worker. Both clients reuse connections. No historical evidence path is a default output destination.

For Linux, build the isolated baseline to `bin/phase04-linux-http`, then:

```sh
docker build -f scripts/api/Dockerfile.comparison -t market-data-comparison:phase04 bin/api-dist
mkdir -p /tmp/phase04-linux-results
docker run --network none --memory 1000000000 --memory-swap 1000000000 \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  --user "$(id -u):$(id -g)" --tmpfs /tmp:rw,nosuid,noexec,size=64m \
  -v "$PWD:/workspace:ro" -v /tmp/phase04-linux-results:/results:rw \
  market-data-comparison:phase04 python /workspace/scripts/api/compare_transports.py \
  --baseline /workspace/bin/phase04-linux-http \
  --grpc-binary /workspace/bin/phase04-linux-grpc \
  --fixtures /workspace/bin/phase04-fixtures --output /results
```

The original result mount was `docs/evidence/grpc-migration/phase-04/tcp-linux` and the non-root host-matching UID/GID was 501:20. Runtime has no external network; dependency download occurs only while building the measurement image. This image is separate from the service image.

For capacity:

```sh
CGO_ENABLED=0 GOOS=linux go test -c -o bin/phase04-load ./internal/bootstrap
docker run --network none --memory 1000000000 --memory-swap 1000000000 \
  --read-only --cap-drop ALL --security-opt no-new-privileges --user 65532:65532 \
  -e GOMEMLIMIT=700MiB -e MDS_RELEASE_LOAD=main \
  -v "$PWD/bin/phase04-load:/probe:ro" --entrypoint /probe market-data-service:v1 \
  -test.run '^TestReleaseLoad$' -test.v -test.timeout 15m
```

For the separate overload profile, build `go test -c -o bin/phase04-transport ./internal/transport/grpc`, then run that binary with `-test.count=20 -test.v -test.timeout=3m` and this `-test.run` expression:

```text
^(TestSlowReaderKeepsCapacityUntilReset|TestSlowReaderAbortsAtWriteDeadline|TestForcedCloseAbortsBlockedSend|TestEarlierClientDeadlineAbortsFlowControlledSend|TestCanceledRPCKeepsCapacityThroughNativeSerialization)$
```

The recorded run uses macOS `/usr/bin/time -l`; its maximum resident set size is bytes. Operational saturation is `go test ./internal/bootstrap -run '^TestOperationalHTTPStaysAvailableDuringSaturatedRPCAndShutdown$' -count=20 -v`.

The conversion investigation is separate from the TCP matrix:

```sh
GOMEMLIMIT=700MiB go test ./internal/transport/grpc -run '^$' \
  -bench '^BenchmarkKlineConversionAndEncoding$' -benchtime=3s -count=1 \
  -cpuprofile=/tmp/conversion.cpu.pprof -o bin/phase04-encoding-test
go tool pprof -top -cum -nodecount=25 bin/phase04-encoding-test /tmp/conversion.cpu.pprof
```

## Earlier runs and limitations

- [check-initial.log](check-initial.log) failed on an unchecked measurement-client Close. It was corrected; the full check then passed.
- [installed-initial.log](installed-initial.log) failed on sandboxed Go cache access. [installed-local.log](installed-local.log) exposed an expired funding-event fixture expectation. The existing exact presence test stays intact; the final real-clock composition correctly has no countdown for an expired event.
- [installed-final.log](installed-final.log) exposed the native deadline/reset race. The [independent unchanged-base reproduction](baseline-deadline/result.json) establishes its origin. The final API log includes the narrow client-side assertion correction and all required installations.
- [budget-initial.log](budget-initial.log), [budget-operations.log](budget-operations.log) and [focused.log](focused.log) retain incomplete fixture/compile attempts. Corrected fixtures use valid timestamps, Content-Type and feasible budget settings. Production admission was not changed.
- [comparison-install.log](comparison-install.log) records sandbox DNS failure; [comparison-install-local.log](comparison-install-local.log) records the successful pinned install. [comparison-initial.log](comparison-initial.log) and `tcp/` are diagnostic HTTP-only results from before that client was available. They also overlapped setup work and are not final timing evidence. Use only `tcp-final/` and `tcp-linux/` for comparisons.
- [comparison-image-build.log](comparison-image-build.log) failed because the service build context excludes wheel artifacts. [comparison-image-build-final.log](comparison-image-build-final.log) uses only the wheel directory as its context and passed.
- [capacity-initial.log](capacity-initial.log) is the early capacity pass before other test tooling was added. [capacity.log](capacity.log) is the final run. Both preserve the agreed workload.
- The first sandboxed pprof lookup could not access its cached tool. [conversion-cpu-top-local.log](conversion-cpu-top-local.log) is the successful lookup with local tool access; the binary profile is [conversion.cpu.pprof](conversion.cpu.pprof).

The known historical background-budget test flake was not changed. Final required checks passed. No live exchange success, remote TLS path, arbitrary workload growth or production deployment is inferred from these local results.

## Independent review

The separate extra-high review finished on September 13, 2026 with no open findings. One P2 finding was corrected: the installed-composition manifest could be read before its write completed. The fixture now writes a sibling temporary file and atomically renames it. The full affected API gate passed again in [check-api-review.log](check-api-review.log). The reviewer then confirmed the corrected code and verification report. Only completion status and source/evidence hashes changed after that review; no new test run was needed for these document changes.

The user separately added the English B1 requirement to `AGENTS.md` during final review. That external instruction change is preserved and included in the final documentation hash; it is not a phase 4 implementation change.
