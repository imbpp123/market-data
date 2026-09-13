# gRPC migration verification

Status: complete after independent review and correction of one finding. September 13, 2026. No project commit, push, publication, live exchange call, or deployment was made.

The base is `1965b0ab10b3d8cfdb444b0f7d6b8a10e4eb175a`. [Source hashes, binaries and commands](evidence/grpc-migration/phase-04/checks.json) identify the uncommitted implementation. The [evidence index](evidence/grpc-migration/phase-04/README.md) records reproduction steps and earlier failures. The [HTTP audit](release-verification-v1.md) and phase 1 measurements remain historical and unchanged.

The separate extra-high review found one P2 manifest publication race in the installed-client test fixture. Atomic file publication corrected it, and the affected full API gate passed again. Final review confirmed the corrected source and evidence with no open findings.

## Acceptance

| Requirement | Evidence and result |
| --- | --- |
| Installed generated clients and final composition | [Final API gate](evidence/grpc-migration/phase-04/check-api-review.log): separate Go consumer module, Python 3.13/3.14 wheel and exact local-Git/subdirectory installations outside source. Sync and async clients call all four methods and compare normalized results. The additional fixture uses the production dual-server composition, seeded storage and a local real-adapter boundary. |
| Exact contract values and failures | API and root tests cover absence versus zero, empty filters, nanoseconds, long decimals, counts above 2^53, unknown fields, all application error mappings, invalid wire input and message caps. Both languages decode `response_too_large` and `request_canceled`; native unknown methods, connection failures, deadlines and resets need no detail. |
| Combined budgets and cache | [Focused tests](evidence/grpc-migration/phase-04/focused-corrected.log): Binance common threshold and strict operation share reject missing work through RPC; valid pages, snapshots and warm candles stay readable. Cooldown returns `upstream_unavailable` when it cannot fit the operation lifetime; expiry restores work. Bybit remains independent. These tests use real adapters and controlled time, without exchange traffic. |
| Shared partial fills and warm reads | The Linux capacity run makes exactly 600 missing-page attempts and zero warm attempts. Installed Python also exercises four async callers on one partly cached range, then complete cached reads. The final composition asserts one total missing-page attempt across all installed Python runs. Later package runs reuse that completed cache; their first wave is already warm. These small client checks include channel setup and are not steady-state timing results. |
| Admission, slow readers, cancellation and shutdown | [Separate overload profile](evidence/grpc-migration/phase-04/overload-slow-readers.log): five native scenarios repeated 20 times, all 100 pass. Zero-window HTTP/2 readers hold capacity until reset/deadline; forced close and cancellation release ownership. Peak test-process RSS is 59,752,448 bytes. This uses its own 1,000-row long-decimal fixture, not the ordinary capacity workload. |
| Operational independence | [Twenty saturation repetitions](evidence/grpc-migration/phase-04/operations-saturation.log): each holds one admitted RPC, rejects 20 excess calls and checks health, readiness, enabled metrics and debug media/content before bounded shutdown. Existing bind-failure and worker/fill shutdown tests also pass. |
| Container and executable | [Docker verification](evidence/grpc-migration/phase-04/docker-verify.log): all four RPCs return `UNAVAILABLE/data_not_ready`, old HTTP data routes return 404, health/ready return 200, hardening and 1 GB limits pass. Internal-network SIGTERM exits 0 in 0.142 seconds. |
| Required project gates | `make check`, `make vet`, `make check-api`, `make docker-build`, `make docker-verify`, and `make release-load` pass. Final measurement additions have focused tests, formatting/lint and the affected API gate. No required gate is skipped. |

## Capacity

The [final Linux run](evidence/grpc-migration/phase-04/capacity.log) uses real storage, bounded fill coordination, actual gRPC encoding and four separate local TCP channels. The process includes both the server and its four Go clients. Providers are synthetic and exclude exchange pacing.

The profile keeps 600 series and finishes with 600,000 retained closed candles. Each snapshot type has 20,000 rows with populated optional values. Candle decimals have 25 characters; counters exceed 2^53. It makes 2,400 partial-fill and 2,400 warm candle requests plus 144 full snapshot RPCs. It also checks rolling cleanup, idle cleanup, late-write rejection and fill shutdown.

| Metric | Final result |
| --- | ---: |
| Container memory / memory-plus-swap limit | 1,000,000,000 / 1,000,000,000 bytes |
| GOMEMLIMIT | 700 MiB |
| Peak process RSS, Linux VmHWM | **775,417,856 bytes** |
| Required peak RSS ceiling | 800,000,000 bytes |
| Retained Go heap after fills and GC | 501,295,576 bytes |
| Partial fills: attempts; p50 / p95 / p99 | 600; 20.31 / 39.27 / 44.21 ms |
| Warm reads: attempts; p50 / p95 / p99 | 0; 10.95 / 19.25 / 25.90 ms |
| Result | PASS; exit 0; no OOM kill |

VmHWM is process RSS, not Go heap or the runtime's soft memory target. Container peak usage was not collected; its enforced limit and OOM state are recorded separately. The capacity probe replaces the image entry point and has no service configuration mount. Its inherited image healthcheck failed three times because `/etc/market-data/config.yaml` was absent, so that probe container is marked unhealthy in the raw inspection. The probe itself passed and exited 0; production image health/readiness passed separately in `make docker-verify`. The earlier diagnostic Linux run also passed at 754,057,216 bytes. The final run leaves about 24.6 MB below the engineering ceiling; four synthetic clients do not prove that every legal 64-caller workload fits.

## TCP comparison

Both [macOS results](evidence/grpc-migration/phase-04/tcp-final/comparison.json) and [Linux results](evidence/grpc-migration/phase-04/tcp-linux/comparison.json) contain 108 cases: nine fixtures × two protocols × Go/Python sync/Python async × one/four clients. Linux runs in a 1 GB, network-none container, with separate server and client processes. Loopback TCP remains available inside that network namespace.

HTTP uses the byte-identical archived handlers and fixed domain fixtures from an isolated base checkout. Its only [instrumentation patch](evidence/grpc-migration/phase-04/baseline-instrumentation.patch) adds an explicit GC command between cases. Before timing, every actual HTTP response matches the saved phase 1 body, and every actual gRPC response matches the fixed Protobuf body. Normalized values, optional presence, ordering and counts match across formats. Every measured response also has a checked hash.

Each worker reuses one connection/channel, performs three warmups and takes 20 measured samples. Four-client rows contain 80 timed samples and 92 calls including warmup. Latency uses the sorted sample at index `floor(n*p)` for p50/p95/p99; p99 is only a sample-tail observation. CPU, allocations and connection counters include connection setup, warmup, verification and shutdown. Message sizes are separate from connection bytes. Connection counters measure actual socket reads/writes, including HTTP/2 framing and headers, but exclude TCP/IP headers, ACKs and retransmissions. Plaintext and no compression are used for both protocols.

**Client timing boundaries differ:** HTTP reads the full body and computes SHA256 without parsing JSON. Generated gRPC clients decode, then deterministically re-marshal and hash for verification. Client ratios therefore include different verification costs; they do not isolate decoding cost or predict a real bot's speed. Server CPU is recorded separately. Go records allocations; Python allocation counts are unavailable. Server heap after explicit GC is separate from cumulative process peak RSS. GC between cases is outside each recorded CPU delta.

These comparison readers return fixed in-memory slices. They exclude application/storage work and exchange work; they are not measurements of cache hits or partial fills. Actual storage/fill behavior is covered above. Encoding-only files measure Protobuf marshal; HTTP handler-only files also include validation, conversion and JSON encoding. Do not compare those as identical encoding operations.

| Response fixture | JSON bytes | Protobuf bytes | Reduction |
| --- | ---: | ---: | ---: |
| 1 candle | 452 | 233 | 48.5% |
| 100 candles | 44,111 | 20,330 | **53.9%** |
| 1,000 candles | 441,011 | 203,030 | **54.0%** |
| 20,000 instruments | 8,740,011 | 4,240,000 | 51.5% |
| 20,000 tickers | 7,440,011 | 4,160,000 | 44.1% |
| 20,000 statistics rows | 6,800,011 | 3,820,000 | 43.8% |

All nine responses fit the 16 MiB cap. Both required candle-size reductions pass. Small snapshot results, request sizes, actual connection bytes, raw samples and per-process costs are in the JSON records.

Selected four-client p95 results in milliseconds, with the client-boundary limit above:

| Fixture / client | macOS HTTP → gRPC | Linux HTTP → gRPC |
| --- | ---: | ---: |
| 1,000 candles / Go | 3.97 → 5.24 | 10.90 → 7.13 |
| 1,000 candles / Python sync | 3.46 → 5.50 | 4.86 → 17.98 |
| 1,000 candles / Python async | 3.87 → 5.52 | 6.45 → 7.17 |
| Full instruments / Go | 60.39 → 84.83 | 75.07 → 190.36 |
| Full tickers / Python async | 61.04 → 72.67 | 79.27 → 91.48 |
| Full statistics / Python async | 53.95 → 77.85 | 83.57 → 94.79 |

There is no general latency or CPU improvement. For 92 macOS Go requests of 1,000 candles, server CPU rises from 0.290 to 0.362 seconds; Linux full-instrument Go server CPU rises from 6.476 to 8.688 seconds. These are regressions, not hidden acceptance failures: the approved gate requires smaller messages and bounded capacity, not a fixed speedup.

The [isolated conversion benchmark](evidence/grpc-migration/phase-04/conversion-profile.log) investigates this cost: bounded mapping plus encoding of the same 1,000 candles takes 2.377 ms, allocates 2,318,591 bytes and makes 58,014 allocations per call. Pure Protobuf marshal has a 0.279 ms macOS sample median. The [CPU profile](evidence/grpc-migration/phase-04/conversion-cpu-top-local.log) identifies decimal conversion, allocation and GC work. Code inspection confirms a linear running message-size check, not repeated whole-response sizing. Smaller encoded output does not remove conversion, safety bounds, HTTP/2 handling or generated-client allocations. No unrelated performance refactor was made.

## Environment and limits

Host: Apple M1 Pro, eight CPUs, 16 GiB RAM, macOS 26.6.2 / Darwin 25.6 arm64. Go is 1.27.1. Docker Desktop is 4.90.0 with Engine 29.7.2 and Linux 7.0.12 aarch64. Host comparison uses Python 3.13.12; installed-client gates also use Python 3.14. Linux comparison uses Python 3.13.15 from a digest-pinned image. Generated runtime dependencies remain pinned: grpc-go/grpcio 1.76.0, Go Protobuf 1.36.10 and Python Protobuf 6.33.5. Tool, image and binary identities are in the evidence manifest.

Host and Linux results are separate observations. The Linux comparison sets GOMEMLIMIT=700MiB for Go processes; the host comparison uses the default runtime target. No CPU quota is imposed. Scheduler noise and the short sample tail limit latency conclusions. Repeated full snapshots and conversion remain a CPU/latency cost.

The native deadline test has a verified pre-existing runtime race on the unchanged base: 194 of 200 calls return `DEADLINE_EXCEEDED`, while six receive a `CANCELLED` HTTP/2 reset code 8 after the deadline. [Independent reproduction](evidence/grpc-migration/phase-04/baseline-deadline/result.json) is retained. The client test accepts only that exact reset text, absent details and elapsed deadline branch; other errors still fail. Application deadline mapping remains strict. Earlier failed runs remain visible.

These tests use controlled fixtures and local adapters, without live exchange availability or a secured remote path. The service has no native TLS/authentication. Publishing an image, package or release and choosing/verifying a remote encrypted path remain separate operator actions.
