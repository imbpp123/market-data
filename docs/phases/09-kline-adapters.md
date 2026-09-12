# 09 — Candle exchange adapters

Status: implemented. Dependencies: [05](05-upstream-admission-and-retries.md), [06](06-instruments.md), [08](08-kline-planning.md). Next: [10](10-kline-cache-and-api.md).

## Outcome

Both exchange adapters return exact, validated candle pages and the internal fetch evidence needed by the cache. Source: [specification](../technical-specification-v1.md), sections 3, 8–9, 14–16, and 30.

## Work

- Implement spot/linear kline paths for both exchanges, using the validated series context and adapter-owned interval maps. SupportedTimeframes returns a safe copy from the same mapping table.
- Convert planner half-open bounds to the actual endpoint parameters. Explicitly send the planned limit; charge Binance weight from that value, not from returned row count or the config maximum.
- Parse original decimal strings and integer milliseconds exactly. Enforce required row fields, OHLC bounds, nonnegative volumes, and each exchange's trade-count contract.
- Convert Binance inclusive close time by adding 1ms with overflow protection and verify the Timeframe boundary. Derive Bybit close time from the same Timeframe rules.
- Validate Bybit category/symbol, sort ascending by OpenTime, and reject duplicate times or any malformed used field for the entire page. Do not synthesize data for empty responses.
- Preserve response receipt time and internal request-start evidence. Keep pagination bounded, consistent with the series context, and visible to the shared attempt/accounting mechanism. Assign one clear owner to pagination; do not add an invisible SDK retry/page loop.

## Tests and checks

- All mapping fields and endpoint/query paths for the four exchange/market pairs using httptest.Server and the selected SDK or justified exact-decoding path.
- Bybit turnover versus Binance close-time tuple position; one-row and gapped responses; unused extra fields do not alter normalization.
- Exact long decimals, zeros, missing/null/invalid values, negative values, OHLC violations, timestamp/count overflow, and required integer precision.
- Bybit trade count stays null; Binance zero stays an integer zero and missing/fractional counts fail.
- All interval mappings, case-sensitive 1m/1M, market-specific support, monthly and confirmed weekly/multi-day boundaries, and wrong timestamp units rejected.
- Wrong category/symbol, descending/unordered rows, duplicates, empty pages, failed pages, and context cancellation.
- Requested limit and actual cost match each planned range. Every page and retry is admitted separately, with no SDK types exposed outside infrastructure.

## Exit criteria

Local adapter contract tests prove request construction and normalization. A failed page cannot become a successful partial page, and internal freshness evidence survives into storage without appearing in HTTP JSON.

## Delivered

- `binance.NewKlineProvider` and `bybit.NewKlineProvider` implement the application candle provider contract for spot and linear. Interval lists and request conversion share adapter-owned tables; callers receive safe copies.
- Shared exact tuple normalization validates required fields, OHLC/volume bounds, integer timestamps and counts, calendar boundaries, page size, range membership, and duplicate times. Bybit envelopes also validate category and symbol. Unused tuple fields do not change normalization.
- Both adapters use the existing original-body capture. Binance uses the pinned SDK raw-message request path. Bybit candle requests use a direct HTTP path through the same admitted transport: the SDK generic decoder rejects valid JSON numbers outside the float64 range before returning the captured body. Candle normalization therefore receives the original body without depending on SDK numeric decoding. Regression tests cover numbers such as `1e309`, both decimal expansion limits, and ignored extra numeric fields.
- Every call sends one planned page with an explicit limit and an inclusive end at `To - 1ms`. The planner owns page boundaries; the phase 10 fill service will own page execution and one shared operation context. Adapters add no retry or pagination loop.
- Local tests cover all four endpoint paths, every interval mapping, twelve captured calendar pages, exact decimal/count cases, malformed pages, cancellation, configured limits, all Binance linear weight tiers, and retries sharing a total attempt allowance. Storage/planner integration checks distinguish a request crossing close from a successful retry starting after close.

Validation: `make check` (formatting, build/config, lint including vet, unit tests, race tests). No live exchange requests are needed for these checks. Candle cache fills and the public kline endpoint remain phase 10 work.
