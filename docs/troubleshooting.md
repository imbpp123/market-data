# Troubleshooting

Start with the returned gRPC status and `ErrorDetail.reason`, if present. The complete mapping is in the [API guide](api.md#errors). For configuration changes, validate the file before restarting.

## First checks

Check process status and recent logs:

```sh
curl -i 'http://localhost:8080/health'
curl -i 'http://localhost:8080/ready'
docker compose logs --tail=100
```

For a local executable, read its stderr logs instead of Compose logs. Confirm the version, enabled exchange/market, listener address, and exact request. `/ready` returning 200 does not mean exchange data is ready or fresh.

## Configuration or startup fails

Run validation with the same file and environment as the service:

```sh
./bin/market-data-service -config config/config-v1.yaml -check-config
```

The error identifies the affected setting. Check spelling, types, positive durations, and related bounds against the [configuration reference](../config/config-v1.yaml). Empty values are not the same as omitted values. Environment lists must be JSON arrays. YAML does not expand environment placeholders.

Remove obsolete settings such as flat `server.host`/`server.port` and `observability.stats.log_interval`. Use `server.grpc` and `server.http`. Do not set both Sentry DSN aliases.

If validation passes but binding fails, check whether either port is already in use. The service needs both listeners. For containers, confirm that the mounted file exists and is readable by UID 65532. Host `MDS_` variables are not automatically forwarded by Compose.

## Cannot connect to the API

Use port 9090 for gRPC and 8080 for operational HTTP, unless configured otherwise. Market-data HTTP paths return 404. Reflection is disabled; `grpcurl` needs `-protoset api/descriptor.binpb`. Use a descriptor or client from the matching service version.

Check published ports and listener hosts. Inside another container, `localhost` refers to that container; use the service's container network address. A remote proxy must support gRPC HTTP/2 and trailers. The service itself has no TLS, so use `-plaintext` only for the trusted connection shown in the quick start.

## Data is not ready

`UNAVAILABLE / data_not_ready` means a selected snapshot has not completed its first successful refresh. For candles, the instrument catalog must be ready first.

Check collector logs for the selected exchange and market. Verify outbound network access and any exchange restrictions on the deployment IP. Narrow a snapshot request to one pair to identify which scope is waiting; an unready selected pair fails the whole combined response.

Retry after collection succeeds. A successful empty snapshot returns an empty list, so repeatedly receiving `data_not_ready` is not evidence of an empty market.

## Data is old

Check `updated_at` for instruments and `fetched_at` for tickers, statistics, and candles. Cache reads never advance these times. Failed refreshes keep old snapshots, and there is no automatic expiry.

Look for upstream failures, exhausted operation shares, cooldowns, and invalid source data. Compare the collector's last-success time with the host clock. Keep the host clock synchronized. Restarting discards useful cache data and cannot reset exchange limits.

An old timestamp on a confirmed closed candle is normal: the service reuses it without fetching again. A funding countdown becoming absent can mean the known event time has passed and no new schedule has arrived.

## Requests are overloaded or time out

`service_overloaded` can mean caller capacity, fill capacity, upstream queue capacity, an admission wait limit, or Binance budget rejection. Check [monitoring](operations.md#monitoring) before changing limits.

Reduce concurrent cold requests. Reuse client channels and spread initial loads over time. Request only the history needed. Cached complete ranges need no exchange calls, but still consume local request capacity. Raising caller limits does not create exchange budget.

`request_timeout` means the call did not finish within its service or client deadline. Pages and retries share that time. `upstream_attempt_limit` means the bounded attempt count was exhausted. Inspect exchange errors and page settings; an unchanged retry loop can keep exhausting the same budget.

For `response_too_large`, narrow snapshot filters or request fewer candles. Also set the client's receive cap to 16 MiB. A native client message-size failure may not contain `ErrorDetail`.

## The exchange blocks requests

Rate-limit signals create cooldowns shared by the affected scope. Check logs and, when enabled, Binance [admission diagnostics](operations.md#binance-admission-diagnostics). A local budget rejection and a real exchange cooldown are different causes.

Wait for the applicable budget or cooldown to clear. Check for other exchange clients sharing the outgoing IP. Do not restart to bypass the wait: local counters reset, but exchange usage or bans can remain. Binance 418 can produce a fallback wait of 72 hours.

`catalog_refresh_failed` means a Binance limit refresh failed. The controller keeps its last valid limits. It does not mean that the exchange budget is exhausted.

## Candle range fails

| Reason | Check and action |
| --- | --- |
| `invalid_interval` | Use an exact [supported interval](data-model.md#intervals-and-calendars), including case. |
| `invalid_range` | Align both UTC boundaries, keep `from <= to`, and do not extend beyond the current candle's end. |
| `request_too_large` | Request fewer slots. The default maximum is 1,000, including an explicitly requested open candle. |
| `range_out_of_retention` | Move the start into the current history window. A shorter old request can still be too old. |
| `symbol_not_found` | Check the exact symbol in `ListInstruments` for the same exchange and market. There is no historical symbol lookup. |
| `incomplete_data` | Check for pre-listing periods, missing exchange candles, or pages that return no new data. Choose a range the exchange can supply. |

The service never fills gaps with synthetic candles or returns a trimmed successful range. Previously fetched valid pages stay cached after an error.

## Monitoring endpoint returns 404

`/metrics` and `/debug/stats` are disabled by default. Enable the relevant [monitoring settings](configuration.md#logs-and-optional-monitoring) and restart. Check the configured Prometheus path if it differs from `/metrics`. These routes use the operational HTTP port and accept GET.

## Container exits or uses too much memory

Inspect container state and logs:

```sh
docker compose ps -a
docker compose logs --tail=100
docker stats --no-stream
```

Compose sets a 1,000,000,000-byte memory limit. `GOMEMLIMIT=700MiB` is a soft runtime target, not an RSS cap. Check the container's termination reason before assuming an out-of-memory failure.

The history limit is per series. Many symbols and intervals can fill much more memory than one client expects. Reduce unnecessary series or history, then measure the actual workload. See [capacity checks](operations.md#container-and-capacity-checks). Compose has no automatic restart policy.

## Report a problem

Include the service version, runtime or image, exact request, expected result, actual status/reason, and relevant timestamps. Add a minimal configuration and sanitized logs. Remove credentials, DSNs, and other private values. State whether the problem occurs with one exchange/market or several.

[Documentation index](../README.md#documentation)
