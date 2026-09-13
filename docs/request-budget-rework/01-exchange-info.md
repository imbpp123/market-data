# Phase 1. Read the complete Spot exchangeInfo response

Status: approved by the user. Implementation has not started. Depends on no other phase.

See the [phase list](README.md) and [main specification](../request-budget-rework-specification.md). This phase fixes response loading, not the limiter rules.

## What we will change

1. Send `showPermissionSets=false` when loading Spot exchangeInfo. This is the proposed fix for unused permission data.
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

If the proposed parameter is not enough, the body-limit or decoder change needs review before extending this phase.
