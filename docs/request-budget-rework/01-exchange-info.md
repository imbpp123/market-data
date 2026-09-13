# Phase 1. Read the complete Spot exchangeInfo response

Status: complete on September 13, 2026. Depends on no other phase. Results are recorded below.

See the [phase list](README.md) and [main specification](../request-budget-rework-specification.md). This phase fixes response loading, not the limiter rules.

## What we will change

1. Send `showPermissionSets=false` when loading Spot exchangeInfo. This removes unused permission data.
2. Keep the full catalog. Do not add symbol, status, or permission filters. Keep the fields needed for instruments and rate limits.
3. Keep the current 16 MiB limit on the decoded body. A small compressed response must not bypass this limit.
4. Measure the complete response size and loading memory. Compare the symbol set, required instrument fields, and rate-limit list with the normal response. Account for catalog changes between live calls when comparing them.
5. If the response still does not fit, report the measured size and propose a finite limit or bounded decoder. Do not silently remove memory protection.

The main code areas are the Binance instrument request and its SDK path. Existing transport body checks should remain in use. USDⓈ-M and Bybit requests must keep their current parameters.

## Test cases

| Case | Expected result |
| --- | --- |
| Spot catalog request | The HTTP request contains `showPermissionSets=false` and no new catalog filters. One fetch creates one accounted HTTP attempt. |
| Catalog with several symbols and different statuses | All valid symbols are returned. Required decimal fields and statuses keep their values. |
| Response without permission-set data | Instrument parsing and rate-limit parsing still work. |
| Valid rate-limit list beside valid symbols | The controller receives the limits from the same response. No second HTTP call is needed. |
| Plain or compressed body exactly at the configured size limit | The complete valid body is accepted. Cover gzip, deflate, and Brotli with the existing body tests. |
| Plain or decoded body over the limit | Loading fails with invalid upstream data. No partial catalog is returned. |
| Broken compressed body or truncated JSON | Loading fails clearly. The previous published snapshot stays available. |
| Missing symbols, duplicate symbols, or an invalid row after a valid row | The whole instrument result fails; no partial success is published. |
| USDⓈ-M catalog request | The Spot-only parameter is absent. Funding metadata loading still works. |

## Measurements and completion

During implementation, record the request options, date, HTTP result, decoded bytes, symbol count, and memory measurement method. Store small results in the phase report; do not commit a large live response only as evidence.

Completion requires passing adapter and transport tests plus evidence that the complete Spot catalog fits the selected bounds. If public Binance access is unavailable, record that limitation. Local tests alone do not prove the current live response size.

## Detail still to decide

The measured response fits the existing limit. No body-limit or decoder change is needed.

## Implementation report

The Spot provider passes `showPermissionSets=false` through the existing SDK client. It adds no symbol, status, or permission filter. The private catalog helper now accepts query parameters; USDⓈ-M still passes no parameters. Transport decoding, the default 16 MiB bound, and limit-accounting rules are unchanged.

### Live response comparison

Both requests used `GET https://api.binance.com/api/v3/exchangeInfo`, with no credentials or catalog filters. `curl --compressed` decoded the downloaded response. The table gives file sizes after decoding and curl's downloaded-byte count before decoding. Capture times are local file completion times in UTC on September 13, 2026.

| Query | Captured at UTC | HTTP | Downloaded bytes | Decoded bytes | Symbols |
| --- | --- | --- | --- | --- | --- |
| `showPermissionSets=false` | 09:30:52 | 200 | 143,966 | 6,703,095 | 3,698 |
| None | 09:31:13 | 200 | 318,870 | 17,589,070 | 3,698 |

The normal response exceeds 16,777,216 bytes by 811,854 bytes. The reduced response has 10,074,121 bytes of headroom. Both contain 3,698 unique symbols. Comparison by symbol found no added or removed symbols and no changed symbol fields after excluding `permissionSets`. This includes all instrument identity, status, and filter fields. The `rateLimits` lists are equal: weight 6,000/minute, raw requests 300,000/5 minutes, and order limits of 100/10 seconds and 200,000/day. Order limits remain ignored by admission.

The calls were 21 seconds apart. No catalog change affected this comparison; `serverTime` was not compared. Large live bodies are kept outside the repository. SHA-256 values identify the measured files:

- Reduced: `3a3e8a5c5598b5f0ebf42b7781239f87bfed4926e7397dca7f3368ba2801b7f5`.
- Normal: `7d6ca688f1bd305457f28ec3fb49b9dddb5acbbee4715491b234761cb825f123`.

The parameter semantics were also checked against the [official Spot REST reference](https://github.com/binance/binance-spot-api-docs/blob/master/rest-api.md#exchange-information). Public access worked after allowing network access outside the shell sandbox.

### Loading memory

The opt-in `BenchmarkSpotExchangeInfo` replays the saved reduced response through a local HTTP server, the real admitted transport and SDK, instrument normalization, and memory snapshot publication. Every sample creates fresh controller, client, and repository state. File loading and server setup are outside allocation timing; the file and server are still part of process RSS. No public exchange calls occur during the benchmark.

Run on macOS arm64, Apple M1 Pro, Go 1.27.1, using the default body limit and five samples:

```sh
go test -c -o /tmp/market-data-binance.test ./internal/infrastructure/exchange/binance
MDS_SPOT_EXCHANGE_INFO_FILE=/tmp/market-data-spot-reduced.json \
  /usr/bin/time -l /tmp/market-data-binance.test \
  -test.run '^$' -test.bench '^BenchmarkSpotExchangeInfo$' \
  -test.benchtime 5x -test.benchmem
```

Save the complete reduced response at the specified path before running this command. The benchmark skips when the environment variable is absent.

| Measurement | Result |
| --- | --- |
| Time per complete load and publication | 82,034,633 ns |
| Allocated bytes per load | 62,680,620 |
| Allocations per load | 213,981 |
| Maximum process RSS, reported by macOS `time -l` | 75,874,304 bytes |
| Peak memory footprint, reported by macOS `time -l` | 64,471,712 bytes |

All five loads passed with one accounted HTTP attempt each. Total allocated bytes are not retained heap or peak RSS. This result verifies this catalog-loading path within the selected bounds; it is not a new whole-service capacity test. Future catalog growth remains a risk, and oversized responses still fail.

### Changed files and validation

- `internal/infrastructure/exchange/binance/instruments.go`, `instruments_spot.go`, and `instruments_linear.go`: pass the Spot-only query without changing public interfaces.
- `internal/infrastructure/exchange/binance/exchange_info_test.go`: verify the exact SDK request, all returned symbols and decimal fields, one attempt, and snapshot preservation after oversized, broken gzip, truncated gzip, and truncated JSON responses.
- `internal/infrastructure/exchange/binance/exchange_info_benchmark_test.go`: add the local full-catalog memory replay.
- `internal/infrastructure/exchange/upstream/response_test.go`: verify limits from the same catalog response and one weight-20 charge.
- This report, the phase list, the main rework specification, and the root README record phase 1 completion.

The new request regression test failed before the fix because the query was empty. After implementation, `make check` passed formatting, build, example configuration, lint (zero issues), all tests, and race tests. `make vet` and `git diff --check` also passed. Existing tests cover exact plain/gzip/deflate/Brotli bounds, decoded overflow, malformed and duplicate symbols, invalid rows after valid rows, and USDⓈ-M funding with empty query parameters. Bybit regression tests passed unchanged.

Truncated catalog JSON still uses the existing `upstream_unavailable` classification from limit parsing. Gzip read failures can use `upstream_error`; oversized decoded bodies use `invalid_upstream_data`. All preserve the published snapshot. Error and limiter policy changes belong to later phases.
