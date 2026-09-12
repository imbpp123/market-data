# 03 — Domain and application contracts

Status: complete, September 12, 2026. Dependencies: [02](02-bootstrap-and-configuration.md), calendar decisions from [01](01-specification-decisions.md). Next: [04](04-memory-repositories.md).

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

## Implementation

- `internal/domain/market.go` defines exact models and canonical enums using shopspring/decimal v1.4.0. Models have no transport tags or exchange SDK types.
- `internal/domain/timeframe.go` defines exact parsing, UTC floor/boundary validation, signed slot shifts, bounded counting, and the common history cutoff. Monthly arithmetic uses calendar month indexes. Calendar calculations accept years 1–9999, including pre-epoch retention cutoffs; public request validation must still reject pre-epoch input. Arithmetic outside this range fails explicitly. Calendar alignment does not enable an endpoint interval: application support checks must use provider capabilities. Unknown scopes and unconfirmed Bybit 3d alignment fail.
- Consumer-owned contracts live in `internal/application/{instrument,ticker,marketstats,kline}`. Snapshot filters carry explicit enabled scopes; an empty scope list selects nothing. Repository lists check every selected scope's readiness before row filters. Context cancellation, atomic replacement, independent branch publication, and safe ownership are part of each contract.
- `kline.Stored` carries successful HTTP attempt start time alongside the domain candle. Storage and planning share this metadata. Final evidence requires request start at or after close. Intermediate writes order by request start and then receipt time; ties preserve the stored row. Confirmed final rows stay immutable. The planner and fill coordinator are deferred to phases 08 and 10.
- `ticker.ReadModelBuilder` takes an injected clock and reads it once per response. It returns owned optional values and a duration countdown without modifying cached timestamps or depending on instruments. HTTP DTOs and integer-second serialization are phase 07 work.
- Stable application errors support wrapping and code lookup. Route/method errors and HTTP status mapping remain transport concerns. Providers translate upstream failures into application errors; cancellation preserves context error identity.

Verification covers exact enums/timeframes, fixed and calendar slots, all twelve saved calendar fixtures' expected boundaries, leap years, UTC offsets, invalid alignment, overflow, 1,000/1,001-slot bounds, history cutoff movement, and funding read behavior. Fixture replay here verifies calendar rules only; raw exchange normalization still needs phase 09 adapter tests. Interface behavior is specified here and will be exercised against concrete repositories/providers in their implementation phases.

Executed checks: `make check` passed on the pinned Go 1.27.1 toolchain (formatting, build, example configuration, standard lint including govet, unit tests, and race tests). Import inspection found only the standard library, decimal, and inward project dependencies in production domain/application code. `git diff --check` passed.
