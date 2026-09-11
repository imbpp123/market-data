# 07 — Tickers and market statistics

Status: not started. Dependencies: [06](06-instruments.md), statistics/API decisions from [01](01-specification-decisions.md). Next: [08](08-kline-planning.md).

## Outcome

Current ticker and 24h statistics APIs backed only by independent snapshots. Source: [specification](../technical-specification-v1.md), sections 6–7, 12, 21–24, 36–37, 49, and 57–60.

## Work

- Implement Bybit's single response decode and two independent normalizations. A transport/envelope error blocks both; a normalization or write error in one branch does not prevent the other from publishing.
- Implement Binance bulk price/book joins for spot and price/book/premiumIndex joins for linear. Price defines the symbol set; missing optional records become null. Never mix cycles or call 24hr from the ticker collector.
- Implement Binance FULL 24hr statistics via the verified bulk or complete bounded batching path. Keep ticker and statistics collectors, deadlines, repositories, and operation budgets independent.
- Use capability-driven orchestration. Ticker runs continuously within admission/backoff limits with one unfinished cycle per scope. Binance statistics fetches immediately and waits the configured interval after completion. Bybit has no separate statistics worker, interval, or fetch fallback.
- Implement GET /api/v1/tickers and GET /api/v1/market-stats. Apply filters and agreed DTO/error rules; compute funding countdown at read time from one clock value.
- Enforce the exact window=24h contract before cache access. Select enabled scopes, distinguish uninitialized from successful empty snapshots, and return statistics in exchange/market/symbol order.
- Count shared Bybit HTTP requests once, and count publication success/errors independently for both branches. Preserve each model's own FetchedAt.

## Tests and checks

- All ticker/statistics fields for four exchange/market combinations, exact decimals, optional quote-pair rules, funding applicability, invalid data, and integer overflow.
- Binance joins tolerate ordering and different symbol sets; duplicate records and a failed second/third source or batch retain the old ticker snapshot.
- PriceChange is a signed difference, never a percentage. Test Bybit missing/zero/invalid previous price and independence from ticker normalization; Binance count distinguishes missing, zero, fractional, negative, and overflow.
- Bybit branch normalization and repository failures are isolated in both directions; a shared envelope failure publishes neither. Binance collector failures are independent.
- Controlled-clock immediate collection, no overlapping cycles, Binance wait after completion, ticker backoff across cycles, canceled admission/waits, and common cooldown.
- Missing statistics window defaults to 24h. Empty/repeated/other forms fail with unsupported_window before cache/upstream. Unknown/disabled scopes fail with invalid_filter.
- Any selected unready statistics scope gives 503 data_not_ready; ready empty/missing symbol gives data: []. Failed refresh and reads retain FetchedAt. Ticker JSON excludes statistics and internal NextFundingAt.
- Concurrent reads during replacement see full snapshots and create no upstream requests. Verify local adapter → collector → repositories → separate HTTP responses.

## Exit criteria

Both current-data APIs work from cache for both exchanges. Different publication outcomes cannot corrupt each other, and all request costs remain under their correct budgets.
