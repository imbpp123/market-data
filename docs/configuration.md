# Configuration

Use [config/config-v1.yaml](../config/config-v1.yaml) as the full reference for supported settings, defaults, and validation rules. The file is runnable, and tests compare it with built-in defaults. This guide covers common changes.

## Load and validate settings

Settings are loaded once at startup in this order:

1. Built-in defaults.
2. YAML from `-config`, if supplied.
3. Explicit `MDS_` environment values.
4. Validation of the final configuration.

Omitted fields keep their defaults. Without `-config`, the service uses defaults and environment values. Changing a file or environment value requires a restart.

Build the executable, then validate without starting listeners or collectors:

```sh
make build
./bin/market-data-service -config config/config-v1.yaml -check-config
```

Unknown or duplicate keys, explicit nulls, wrong types, YAML anchors/aliases, and multiple YAML documents fail validation. Unknown `MDS_` variables fail too. Disabled sections still need valid values. YAML strings are literal: `${NAME}` is not expanded.

## Environment overrides

An environment name is `MDS_` plus the uppercase setting path, with dots replaced by underscores. Lists are JSON arrays and replace the whole list:

```sh
MDS_SERVER_HTTP_PORT=8081 \
MDS_KLINES_MAX_HISTORY_CANDLES=500 \
MDS_EXCHANGES_BYBIT_MARKETS='["linear"]' \
./bin/market-data-service -config config/config-v1.yaml
```

Use `true` or `false` for booleans, base-10 integers for counts, and durations such as `500ms`, `30s`, or `1h30m`. Duration values do not accept days (`1d`). Zero does not mean unlimited.

`MDS_SENTRY_DSN` is an alias for `MDS_OBSERVABILITY_SENTRY_DSN`. Set only one. Supply a real DSN through the environment; do not commit it.

## Exchanges and listeners

Both exchanges and both markets are enabled by default. This small YAML file runs only Binance spot and binds both listeners to loopback:

```yaml
server:
  grpc:
    host: 127.0.0.1
  http:
    host: 127.0.0.1
exchanges:
  binance:
    markets: [spot]
  bybit:
    enabled: false
```

Save it as a local configuration file and pass its path with `-config`. At least one exchange and market must be enabled. The service has no symbol allowlist; snapshot collectors fetch the configured markets.

Default listeners are `0.0.0.0:9090` for gRPC and `0.0.0.0:8080` for operational HTTP. Both must bind successfully. Use `server.grpc` and `server.http`; old flat settings such as `server.host` and `server.port` are rejected. In Docker, keep the published port mapping consistent with the configured container port. See [operations](operations.md#docker-and-compose).

## Candle history

`klines.max_history_candles` defaults to 1,000. It limits both the number of slots in one request and the lookback from the current slot boundary. It is not a limit of 1,000 arbitrary old rows.

| Interval | History covered by 1,000 closed slots |
| --- | --- |
| `1m` | 16 hours 40 minutes |
| `5m` | 3 days 11 hours 20 minutes |
| `1h` | 41 days 16 hours |
| `1M` | 1,000 calendar months |

At each check, the service finds the current slot boundary and steps backwards by the configured count. Older starts fail even if rows remain in memory. Missing slots and pre-listing periods do not extend the window. Explicitly requesting the open candle uses one of the request's allowed slots.

Old candles are removed during writes and by periodic cleanup, every hour by default. `storage.cleanup_interval` changes that schedule. Cleanup does not expire instrument, ticker, or statistics snapshots.

History is bounded per series, not across all symbols and intervals. Increasing the history count or requesting more series increases memory use. A smaller exchange page size may require more allowed attempts to complete a range; related settings are validated together.

## Timeouts and capacity

| Setting | Default | Purpose |
| --- | --- | --- |
| `server.snapshot_timeout` | `5s` | One snapshot request, starting at admission |
| `server.max_snapshot_requests` | `64` | Active requests across all three snapshot methods |
| `klines.request_timeout` | `30s` | One candle caller, including waits |
| `klines.max_callers` | `64` | Active candle callers |
| `klines.fill_timeout` | `30s` | One shared candle load |
| `klines.max_active_fills` | `12` | Shared loads across the process |
| `klines.max_active_fills_per_exchange` | `6` | Shared loads for one exchange |
| `http_client.timeout` | `10s` | One exchange HTTP attempt |
| `server.shutdown_timeout` | `35s` | Total shutdown time |

Client deadlines can end a call earlier. Pages, retries, and joining another load do not restart a caller's timer. The transport allows 5 extra seconds to finish sending, measured from admission plus the request timeout. Shutdown must cover the longest caller or fill lifetime plus that grace.

The full config also bounds message sizes, upstream queues, concurrent HTTP requests, and attempts. Raising caller limits alone does not increase exchange capacity. Test the workload before raising these bounds.

## Exchange request limits

Binance uses `upstream.binance.stop_threshold_percent`, an integer from 1 to 99, with a default of 90. Limits begin with configured values and update from valid exchange catalogs. `catalog_refresh_interval` defaults to `1h`.

An explicitly supplied Binance window `limit` is a user cap, even when equal to its default. An omitted limit can follow an exchange increase. This means the full reference YAML pins those limits when used unchanged; omit individual limits when you want discovery to follow increases. Exchange reductions always apply.

`upstream.safety_margin_percent` applies to Bybit, with a default of 20. It does not change the Binance percentage. Operation shares default to 60% tickers, 30% candles, 5% instruments, and 5% statistics. Bybit shares one fetch for tickers and statistics, so their combined share is 65%.

These settings control local admission, not a guaranteed percentage of actual IP usage. See [request-limit behavior](architecture.md#exchange-request-limits) and [restart limits](operations.md#restarts-and-updates).

## Logs and optional monitoring

JSON logs go to stderr. In-memory statistics collection is enabled by default. The debug endpoint, Prometheus endpoint, and Sentry are disabled by default.

To expose local counters, add these settings to your YAML:

```yaml
observability:
  prometheus:
    enabled: true
  stats:
    endpoint_enabled: true
```

This enables `/metrics` and `/debug/stats` on operational HTTP. Prometheus and debug can each request collection even if `observability.stats.enabled` is false. Collection stops only when all three are disabled. Sentry is independent.

For Sentry, set `observability.sentry.enabled` and supply a DSN. A trace sample rate of zero disables traces while keeping error reports. The removed `observability.stats.log_interval` setting is rejected; there is no periodic statistics logger.

See [monitoring](operations.md#monitoring) for endpoint use and [troubleshooting](troubleshooting.md#configuration-or-startup-fails) for validation failures.

[Documentation index](../README.md#documentation)
