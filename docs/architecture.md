# Architecture

Market Data collects public exchange data, converts it to common models, and serves it through gRPC. All data lives in memory. Snapshot reads use background updates; candle reads load missing data on demand.

Start with the [data model](data-model.md) for terminology and the [API guide](api.md) for client behavior.

## Components

| Component | Responsibility | Source |
| --- | --- | --- |
| Entry point and bootstrap | Load settings, construct dependencies, own startup and shutdown | [cmd](../cmd/market-data-service), [bootstrap](../internal/bootstrap) |
| Domain | Market entities, exact values, and candle calendars | [domain](../internal/domain) |
| Application | Read snapshots, plan and coordinate candle loads, schedule refreshes | [application](../internal/application) |
| Exchange adapters | Call exchange endpoints and normalize their responses | [exchange](../internal/infrastructure/exchange) |
| Upstream controller | Check request budgets, pace requests, handle retries and cooldowns | [upstream](../internal/infrastructure/exchange/upstream) |
| Memory repositories | Replace snapshots and merge candle pages safely | [memory](../internal/infrastructure/storage/memory) |
| Transports | Convert requests, enforce protocol limits, return data and errors | [gRPC](../internal/transport/grpc), [HTTP](../internal/transport/http) |
| Observability | Collect operational counters and export metrics or errors | [observability](../internal/infrastructure/observability) |

Dependencies point inward. Domain code does not know about networks, storage, or Protobuf. Application code uses interfaces defined near its use cases. Infrastructure implements those interfaces. Bootstrap connects the concrete implementations. Generated messages and exchange SDK types stay outside domain and application code.

## Data paths

A snapshot follows this path:

```text
background worker -> request-limit controller -> exchange adapter
                  -> normalize complete result -> replace memory snapshot

client -> gRPC handler -> application reader -> memory snapshot -> response
```

A candle request follows this path:

```text
client -> gRPC handler -> candle service -> validate range and read cache
  complete cache -> response
  missing data   -> shared load -> controller -> exchange -> merge cache
                 -> check the whole requested range again -> response or error
```

A *scope* is an exchange and market pair, such as Binance spot. A candle *series* adds symbol and interval to that scope. A *fill* is a service-owned load of missing or unconfirmed candles for one series.

## Background collection

Each enabled scope has an instrument worker and a ticker worker. Binance also has a separate statistics worker. Workers start after local initialization and do not wait for all other scopes to become ready.

| Data | Collection behavior |
| --- | --- |
| Instruments | Fetch the selected catalog and required metadata. Publish only after every required page succeeds. Wait 10 minutes after completion by default. |
| Binance tickers | Join bulk price and book responses by symbol; linear also uses premium-index funding data. Continue through request admission and failure backoff. |
| Binance statistics | Fetch a separate bulk 24-hour response. Wait 30 seconds after completion by default. |
| Bybit tickers and statistics | Use one ticker fetch for both models. Normalize and publish the two branches independently. |

The Binance price response defines the ticker symbol set. Optional book or funding data comes from the same cycle. Responses from different cycles are not mixed. A failure in a required source preserves the previous ticker snapshot.

Each successful snapshot replaces its previous scope atomically. Readiness is tracked separately from row count, so a successful empty snapshot is ready. A failed refresh preserves old values and timestamps. The three snapshot types have independent readiness. A failed Bybit statistics branch can leave tickers updated while statistics remain older.

Ticker cycles run continuously subject to pacing, budgets, and failure backoff. Interval workers wait after completion; they do not queue missed runs. Temporary failures use bounded retries. Failure backoff persists across cycles and resets after a complete success. A configured refresh interval is not a freshness guarantee.

## Candle loading and cache

The [planner](../internal/application/kline/fetch_planner.go) checks interval support, range shape, slot count, and rolling history before storage or network work. It has no I/O. It uses the same domain calendars as normalization and retention.

The planner identifies missing and unconfirmed slots. It combines them into pages within the exchange page limit. A page may include cached slots between gaps when that reduces requests. The adapter fetches one planned page; the application owns pagination.

Concurrent requests for the same series share one active fill. Its planned range does not grow when a later caller arrives. Each caller checks its own range again after waiting. A partly overlapping request reuses saved rows and may then need another fill for the remaining gap.

Pages run sequentially within one fill and share its deadline and attempt budget. Each actual HTTP attempt, including a retry, consumes budget. Each waiting caller also has a logical attempt limit across the fills it joins; a late joiner conservatively counts work already dispatched by its shared fill.

Canceling a caller stops its wait. The fill belongs to the service and can continue to populate the cache for other callers. It has its own finite lifetime and is canceled at service shutdown.

The cache stores candles by series and opening time. It keeps request-start metadata to distinguish a confirmed candle from a response merely received after close. Older results cannot replace newer intermediate values. Current candles need a refresh for each RPC; passing time alone never confirms cached values.

A load succeeds only when the caller's whole range is available. Short or empty pages cannot mark gaps as complete. A no-progress load returns `incomplete_data`. Valid saved pages survive a later failure; if they already complete a caller's range, that caller can still succeed.

## Retention

Retention uses calendar slots, not a target number of stored rows. The cutoff is the current slot boundary minus `klines.max_history_candles` slots. Rows strictly before it are removed; a row exactly at the cutoff stays.

Repositories prune during writes. A cleanup worker also processes idle series immediately at startup and then waits `storage.cleanup_interval` after each pass. Cutoffs never move backwards, and late writes cannot restore expired data. Empty series are released. One cleanup failure is reported without stopping other scopes.

Requests recheck retention after waiting. A range that became too old fails rather than repeatedly fetching data that cleanup removes. A complete read captured while valid can finish sending after the next boundary. Snapshots are not subject to candle retention.

## Exchange request limits

Every exchange attempt passes through the shared upstream controller, including pages and retries. A collection cycle or fill has one operation context; making a new HTTP request does not reset its deadline or attempt count.

The controller checks endpoint cost, applicable time windows, reserved operation shares, cooldown, pacing, queue space, and HTTP capacity before dispatch. Unknown endpoint costs fail before sending. Response bodies are bounded before unbounded decoding, including after decompression.

Binance spot and linear have separate budget scopes. Bybit spot and linear share an IP request scope. Binance funding metadata also has a separate request-count window. Zero weight does not exempt a request from other applicable windows.

### Binance limits

Configured starting limits apply before discovery. Valid `exchangeInfo` responses update supported weight and request-count windows. Explicit user caps can lower them. Catalog updates do not reset usage; reductions apply immediately. Invalid or older updates keep the last valid catalog. An instrument refresh can install valid limit metadata even if later instrument normalization fails.

Instrument refreshes reuse their catalog response. When the separate catalog refresh interval is due, the worker can make a normal catalog request through the same admission path. It does not send unrestricted recovery probes.

The stop line is the selected limit multiplied by `stop_threshold_percent`, rounded down. With the default 90%, a selected limit of 6,000 gives a stop line of 5,400.

The controller rejects a new positive-cost request when current accounted usage is already above a stop line. At or below the line, one request may cross it. Operation shares are stricter: current local usage plus the next request cost must fit the share. Unused shares are not borrowed. A request that cannot fit even an empty allowance fails as `upstream_unavailable`.

Accounted usage combines local dispatches, in-flight reservations, and valid exchange observations. Only an observation's own request is known to be included in its counter. Smaller or delayed counters do not erase live costs. Conservative overlap can approach twice actual usage. Missing headers do not refund spent units. Inaccurate USD-M price/book weight headers are ignored.

Budget rejection returns immediately without occupying a queue or HTTP slot. Background workers defer until usage expires or state changes. A deferral is not an exchange attempt or a failed refresh. Cache reads remain available.

### Bybit, pacing, and retries

Bybit uses a strict allowance with the configured safety margin, 20% by default. It can wait for budget within bounded queues and deadlines. Its ticker and statistics work shares the same fetch and combined operation share.

Minimum request spacing defaults to 20 ms per Binance scope and 10 ms for the shared Bybit scope. Ready operation lanes take turns; requests within a lane keep their order. Idle time does not accumulate permission for a burst. Waiting for pacing or cooldown does not reserve an HTTP slot.

Retries are bounded per request and per operation. Temporary network failures, attempt timeouts, HTTP 429, and selected server errors (500/502/503/504) can retry. Rate-limit signals also set a shared cooldown. A new signal can extend an existing cooldown, never shorten it. Unrelated access errors do not enter a generic retry loop.

Valid exchange wait metadata is used when available. Fallback waits are 60 seconds for Binance 429 and Bybit 429/10006, 72 hours for Binance 418, and at least 10 minutes for Bybit's access-too-frequent 403. Config validation enforces these minima. Cooldown, caller deadline, operation deadline, and remaining attempts all constrain another attempt.

Usage, discovered limits, and cooldowns are memory-only. Restarting loses them; exchange-side usage and bans can remain. Local checks cannot guarantee actual usage by every process sharing an outgoing IP. See [operations](operations.md#restarts-and-updates).

## gRPC request ownership

The server uses grpc-go `ServeHTTP` with Go's unencrypted HTTP/2 server. A wrapper acquires a process-wide snapshot or candle slot before reading the body. Rejected overload requests do not wait for a body. Accepted calls read one bounded unary frame and require its end before application dispatch.

The application deadline begins at admission. Response conversion and serialization stay inside the caller slot. A slow reader must not release that slot while the server still holds its response. A write deadline bounds sending to the request lifetime plus five seconds, or an earlier client deadline.

The implementation releases a slot only after both native RPC processing and the HTTP/2 stream finish. `stats.End` covers processing; `CloseNotify` covers stream closure. Handler return and request-context cancellation happen too early to replace these signals. Dispatch fencing prevents late application work, and cancellation callbacks are stopped and joined before the writer can be reused.

This behavior depends on pinned runtimes. grpc-go marks `ServeHTTP` experimental, and `CloseNotifier` is deprecated. Runtime changes need the [slow-reader](../internal/transport/grpc/server_test.go), [ownership](../internal/transport/grpc/ownership_test.go), and [input/deadline](../internal/transport/grpc/review_regression_test.go) regressions. The HTTP servers own draining; `grpc.Stop` closes remaining transports. `grpc.GracefulStop` cannot be used here because handler transport draining is unsupported.

A hard write deadline may produce a native stream-reset status instead of `DEADLINE_EXCEEDED`. The pinned Go runtime can send HTTP/2 `INTERNAL_ERROR`; installed Python clients can also see a cancellation reset after their deadline. A writable application timeout still maps to `request_timeout`. See the [installed-client checks](../scripts/api/test_contract.py).

RPC metrics cover processing and local stream completion, including cancellation cleanup. They do not prove that the remote application consumed a response. Supported hooks cannot distinguish every trailer-only failure after handler return from normal closure. Metric labels stay bounded by method, status, and known reason.

## Startup and shutdown

Startup validates settings, creates memory storage and services, binds both listeners, and starts owned workers. Both listeners must bind before readiness becomes true. A bind failure closes any listener already opened. A serve failure makes the process unready and starts normal shutdown.

`/ready` describes this local initialization, not exchange availability. Individual snapshots can remain unready while the process is ready.

SIGINT or SIGTERM clears readiness, closes new RPC admission, and cancels service-owned work. Both servers drain concurrently. Shutdown waits for requests, fills, collectors, and cleanup within one shared timeout, 35 seconds by default. Sentry flush uses only the remaining time.

Cancellation cannot forcibly stop a Go goroutine. A dependency that ignores cancellation keeps its slot until processing ends, even if the stream is already closed. The shutdown owner still returns at its deadline; a permanently stuck dependency may require process exit.

[Documentation index](../README.md#documentation)
