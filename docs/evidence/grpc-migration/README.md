# Phase 1 contract and HTTP baseline evidence

These files record migration phase 1 on September 13, 2026. They do not certify the production gRPC transport or container capacity. The production entry point and configuration still use HTTP.

- `message-sizes.json`: nine fixed fixtures, exact HTTP and Protobuf response lengths, hashes and semantic equality.
- `*.http.json.gz`: complete fixed HTTP responses. Gzip is only archive storage; all measured traffic was uncompressed.
- `http-baseline.json`: 45 measurement rows and 1,980 raw latency samples. It includes Go/Python clients, one/four reused connections, and separate handler-only runs. Source revision, relevant file hashes, dirty-tree status, environment and measurement limits are included.
- `tool-versions.json`: generator, runtime and build tool versions.
- `checks.json`: phase 1 verification results and completed independent review status. One reconstruction-instruction finding was corrected and verified; the [phase report](../../grpc-migration/01-contract-and-clients.md#independent-review-result) records the review scope and limits.
- `reconstruction/`: full raw rerun and assertions for the corrected local-clone instructions; all nine fixed responses and relevant source hashes match the original baseline.

## Message sizes

| Fixture | HTTP response bytes | Protobuf response bytes |
| --- | ---: | ---: |
| 20,000 instruments | 8,740,011 | 4,240,000 |
| 20,000 tickers | 7,440,011 | 4,160,000 |
| 20,000 statistics rows | 6,800,011 | 3,820,000 |
| 1 candle | 452 | 233 |
| 100 candles | 44,111 | 20,330 |
| 1,000 candles | 441,011 | 203,030 |

Full snapshots span four scopes with all optionals populated. Candle decimals are `12345.1234567890123456789`; the large count is `9007199254740993`. Timestamp nanos are `123456789`. Every decoded HTTP row matches its normalized Protobuf row, including whole-second duration names and response-level candle identifiers. The separate interoperability tests also cover absent/zero values and a 1,024-character decimal.

The largest normal fixture is 4,240,000 bytes, below the proposed 16,777,216-byte response cap. The 100/1,000-candle Protobuf responses are smaller than their equivalent JSON responses. This verifies the phase 1 message-size gate, not concurrent send memory or the Linux capacity gate.

## Reproduce

From the phase 1 source, with Go 1.27.1 and Python 3.13/3.14 on PATH:

```sh
make generate-api
make check-api
make api-http-baseline
make check
make vet
```

`make check-api` saves a local versioned wheel under ignored `bin/api-dist/`. Git installs use a temporary local repository and exact fixture commit, outside the source tree. No project commit or tag is created.

The HTTP source base is `5c185e009d487a5a9fdaec66f98ec034c6206ee8`. The measurement used additional phase 1 harness files, identified by hashes in `http-baseline.json`; it was not a clean run of that revision. Concurrent documentation edits are recorded in the dirty-tree inventory and do not belong to this phase's implementation.

After HTTP removal, use a separate local clone with Git metadata. The harness calls `git rev-parse HEAD` and `git status` to record provenance, so an extracted source archive is not sufficient. Run these commands from a saved phase 1 checkout containing the reviewed contract and harness:

```sh
phase1_source="$PWD"
baseline_parent=$(mktemp -d)
baseline_checkout="$baseline_parent/market-data"
git clone --no-hardlinks --no-checkout "$phase1_source" "$baseline_checkout"
git -C "$baseline_checkout" switch --detach 5c185e009d487a5a9fdaec66f98ec034c6206ee8
cp -R "$phase1_source/api" "$baseline_checkout/api"
mkdir -p "$baseline_checkout/scripts"
cp -R "$phase1_source/scripts/api" "$baseline_checkout/scripts/api"
cp "$phase1_source/Makefile" "$baseline_checkout/Makefile"
cd "$baseline_checkout"
python3.13 scripts/api/http_baseline.py
```

The checked-in generated Go files are sufficient for this baseline run; it does not need regeneration or Python package installation. The command writes `docs/evidence/grpc-migration/http-baseline.json`, `message-sizes.json`, and the archived response bodies inside the temporary clone. Keep the resulting evidence before removing that clone.

Compare the relevant source hashes and all nine fixed response hashes with this evidence. The base revision supplies the original handlers/domain and dependencies; the phase 1 overlay supplies the harness and fixed contract. Git records that overlay as modified/untracked files. No project commit or tag is needed, and the active checkout stays unchanged. Future runs must record their own environment and source identity.

## Measurement boundaries and limits

Traffic uses plaintext local HTTP/1.1 over TCP with compression disabled. Each worker owns one reused connection. Latency includes request send, full body read and SHA256 validation. Three warmup calls per worker are excluded from latency samples; CPU, allocations and connection-byte totals include them. Raw before/after counters permit exact deltas.

Connection-byte counters wrap the server's actual `net.Conn.Read/Write`; they include HTTP headers and framing. They exclude TCP/IP headers, ACKs and retransmission overhead. Encoded HTTP request bodies contain zero bytes; request-target length is recorded separately. Message length is never used as the connection-byte count.

Go and Python clients run in separate processes from the server. CPU and peak RSS are separate per process. Go allocations and retained heap are recorded; Python allocation counts are unavailable because enabling tracemalloc would change the timed workload. Server RSS is a cumulative high-water mark across cases. Handler-only timing includes validation, mapping, decimal conversion and JSON encoding, with no TCP or storage. These numbers must not be presented as isolated `json.Marshal` cost.

Readers return fixed in-memory slices. This measures the old handlers and network, excluding application/storage and exchange work. There are 20 samples per worker, so p99 is only a sample-tail observation. The four-client normal profile is included; partial-fill, overload, 600,000-candle retained-cache and bounded Linux container acceptance remain phase 4 work. No gRPC latency or CPU improvement is claimed from these HTTP-only measurements.
