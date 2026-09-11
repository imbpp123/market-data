# 03 — Domain and application contracts

Status: not started. Dependencies: [02](02-bootstrap-and-configuration.md), calendar decisions from [01](01-specification-decisions.md). Next: [04](04-memory-repositories.md).

## Outcome

Define exact market values, calendar rules, and the boundaries consumed by use cases. Source: [specification](../technical-specification-v1.md), sections 3–12, 18, 20, 23–24, and 28–31.

## Work

- Implement Exchange, Market, InstrumentStatus, Instrument, Ticker, MarketStats, Kline, and Timeframe according to the specified fields. Use decimal.Decimal, integer counts, time.Time, and explicit optional values.
- Implement exact canonical timeframe validation and reusable UTC boundary/slot operations. Keep monthly calendar arithmetic separate from fixed durations. Use confirmed anchors without embedding SDK parameters in domain code.
- Separate MarketStats.Window from Timeframe. Implement funding countdown as application read logic using one injected now per response; store NextFundingAt, not a decreasing value.
- Define consumer-owned repository/provider contracts near the relevant application services, using the specification examples as guidance. Include capabilities, independent ticker/statistics branch outcomes, scope readiness, errors, cancellation, and safe ownership of returned values.
- Define how kline storage and planning carry internal request-start/finalization information. Do not add it to the public Kline model or HTTP response.
- Add stable application errors required by the agreed APIs. Keep upstream error types and transport status mapping outside inner layers.

## Tests and checks

- Exact timeframe allowlist, empty/zero/unknown values, whitespace, case, and rejection of aliases such as 60m.
- UTC conversion, day/year transitions, 28/29/30/31-day months, confirmed weekly/multi-day anchors, invalid boundaries, overflow, and bounded slot counting.
- Countdown: absent, future, less than one second, reached/past event, and consistent time across list items. Reads do not change stored timestamps or derive a schedule from FundingInterval.
- Test actual validation and calculation behavior; do not write constructor/getter tests for pure model containers.
- Inspect imports: no SDK, HTTP framework, database, Sentry, or Prometheus dependency in domain/application.

## Exit criteria

Domain rules are deterministic and tested. Contracts cover real use cases without generic repositories or unused layers. Unsupported calendar alignment remains explicitly gated rather than approximated.
