package observability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countStore struct {
	counts map[application.Scope]int
	err    error
}

func (s countStore) CandleCounts(ctx context.Context) (map[application.Scope]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.counts, s.err
}

func testStatistics() *Statistics {
	scope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}
	return &Statistics{Instruments: NewInstruments(), Current: NewCurrent(), Klines: NewKlines(), Exchanges: NewExchanges(), Inventory: countStore{counts: map[application.Scope]int{scope: 7}}, Scopes: []application.Scope{scope}}
}

func TestStatisticsExportsIndependentBybitOutcomes(t *testing.T) {
	stats := testStatistics()
	scope := stats.Scopes[0]
	at := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	stats.Exchanges.Observe(upstream.Event{Scope: upstream.Bybit, Market: scope.Market, Operation: upstream.Tickers, Duration: 250 * time.Millisecond})
	stats.Current.Observe(application.RefreshEvent{Scope: scope, FetchedAt: at, Size: 2})
	stats.Current.Observe(application.RefreshEvent{Scope: scope, Window: 24 * time.Hour, FetchedAt: at.Add(-time.Hour), Size: 1})
	stats.Current.Observe(application.RefreshEvent{Scope: scope, Window: 24 * time.Hour, Error: application.ErrInvalidUpstreamData})
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()

	stats.PrometheusHandler().ServeHTTP(response, request)

	assert.Equal(t, "text/plain; version=0.0.4; charset=utf-8", response.Header().Get("Content-Type"))
	text := response.Body.String()
	assert.Contains(t, text, `exchange_requests_total{exchange="bybit",market="linear",operation="tickers"} 1`)
	assert.Contains(t, text, `market_stats_refresh_total{exchange="bybit",market="linear",window="24h"} 1`)
	assert.Contains(t, text, `market_stats_refresh_errors_total{exchange="bybit",market="linear",window="24h"} 1`)
	assert.Contains(t, text, `kline_count{exchange="bybit",market="linear"} 7`)
	assert.Contains(t, text, `exchange_request_duration_seconds_sum{exchange="bybit",market="linear",operation="tickers"} 0.25`)
	assert.NotContains(t, text, "symbol=")
	assert.Equal(t, 1, strings.Count(text, "# TYPE kline_count gauge\n"))
	assert.Equal(t, at.Add(-time.Hour), stats.Current.Snapshot()[CurrentScope{Scope: scope, Window: 24 * time.Hour}].LastSuccessfulFetch)
	debug := httptest.NewRecorder()
	stats.DebugHandler().ServeHTTP(debug, request)
	var decoded []Sample
	require.NoError(t, json.Unmarshal(debug.Body.Bytes(), &decoded))
	snapshot, err := stats.Snapshot(t.Context())
	require.NoError(t, err)
	assert.Equal(t, snapshot, decoded)
}

func TestExchangeStatisticsAreSafeDuringAggregateReads(t *testing.T) {
	stats := testStatistics()
	var owned sync.WaitGroup
	for range 50 {
		owned.Go(func() {
			stats.Exchanges.Observe(upstream.Event{Scope: upstream.BinanceSpot, Operation: upstream.Instruments, Duration: time.Second})
			stats.Exchanges.Observe(upstream.Event{Scope: upstream.BinanceSpot, Operation: upstream.Instruments, Duration: 2 * time.Second, Error: application.ErrUpstream})
			_, err := stats.Snapshot(t.Context())
			assert.NoError(t, err)
		})
	}
	owned.Wait()

	snapshot := stats.Exchanges.Snapshot()
	key := ExchangeScope{Scope: application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}, Operation: "instruments"}
	assert.Equal(t, ExchangeStats{Requests: 100, Errors: 50, Duration: DurationStats{Count: 100, Sum: 150, Max: 2}}, snapshot[key])
	delete(snapshot, key)
	assert.Len(t, stats.Exchanges.Snapshot(), 1)
}

func TestStatisticsStorageFailureDoesNotReturnPartialMetrics(t *testing.T) {
	stats := testStatistics()
	stats.Inventory = countStore{err: errors.New("storage credential must not leak")}
	cases := []struct {
		name    string
		handler http.Handler
	}{{"debug", stats.DebugHandler()}, {"prometheus", stats.PrometheusHandler()}}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()

			tt.handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

			assert.Equal(t, http.StatusServiceUnavailable, response.Code)
			assert.NotContains(t, response.Body.String(), "credential")
			assert.NotContains(t, response.Body.String(), "kline_count")
		})
	}
}
