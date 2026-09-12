package binance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKPublicPathsUseOneAccountedRequest(t *testing.T) {
	cases := []struct {
		scope  upstream.Scope
		path   string
		kind   upstream.Operation
		params url.Values
		body   string
	}{
		{upstream.BinanceSpot, "/api/v3/exchangeInfo", upstream.Instruments, nil, `{"rateLimits":[],"symbols":[]}`},
		{upstream.BinanceSpot, "/api/v3/ticker/price", upstream.Tickers, nil, `[{"symbol":"BTCUSDT","price":"1.1234567890123456789"}]`},
		{upstream.BinanceSpot, "/api/v3/ticker/bookTicker", upstream.Tickers, nil, `[]`},
		{upstream.BinanceSpot, "/api/v3/ticker/24hr", upstream.MarketStats, url.Values{"type": {"FULL"}}, `[]`},
		{upstream.BinanceSpot, "/api/v3/klines", upstream.Klines, url.Values{"limit": {"1000"}, "symbol": {"BTCUSDT"}, "interval": {"1m"}}, `[[9007199254740993,"1.1234567890123456789"]]`},
		{upstream.BinanceLinear, "/fapi/v1/exchangeInfo", upstream.Instruments, nil, `{"rateLimits":[],"symbols":[]}`},
		{upstream.BinanceLinear, "/fapi/v1/fundingInfo", upstream.Instruments, nil, `[]`},
		{upstream.BinanceLinear, "/fapi/v2/ticker/price", upstream.Tickers, nil, `[]`},
		{upstream.BinanceLinear, "/fapi/v1/ticker/bookTicker", upstream.Tickers, nil, `[]`},
		{upstream.BinanceLinear, "/fapi/v1/premiumIndex", upstream.Tickers, nil, `[]`},
		{upstream.BinanceLinear, "/fapi/v1/ticker/24hr", upstream.MarketStats, nil, `[]`},
		{upstream.BinanceLinear, "/fapi/v1/klines", upstream.Klines, url.Values{"limit": {"1500"}, "symbol": {"BTCUSDT"}, "interval": {"1m"}}, `[[9007199254740993,"1.1234567890123456789"]]`},
	}
	for _, tt := range cases {
		t.Run(tt.path, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, tt.path, r.URL.Path)
				assert.Equal(t, tt.params.Encode(), r.URL.Query().Encode())
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Test-Request", tt.path)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			cfg := config.Defaults()
			controller, err := upstream.New(cfg, upstream.SystemClock{})
			require.NoError(t, err)
			transport, err := upstream.NewTransport(controller, tt.scope, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
			require.NoError(t, err)
			client, err := NewClient(tt.scope, server.URL, transport)
			require.NoError(t, err)
			ctx, cancel, err := controller.Begin(t.Context(), tt.scope, tt.kind)
			require.NoError(t, err)
			defer cancel()
			result, err := client.Fetch(ctx, tt.path, tt.params)
			require.NoError(t, err)
			assert.Equal(t, json.RawMessage(tt.body), result.Body)
			assert.Equal(t, tt.path, result.Header.Get("X-Test-Request"))
			assert.False(t, result.FetchedAt.Before(result.StartedAt))
			assert.Equal(t, int64(1), calls.Load())
			assert.Equal(t, 1, controller.Attempts(ctx))
		})
	}
}

func TestSDKErrorsKeepHeadersAndDisableNestedRetries(t *testing.T) {
	for _, scope := range []upstream.Scope{upstream.BinanceSpot, upstream.BinanceLinear} {
		for _, status := range []int{429, 418, 500} {
			t.Run(string(scope)+http.StatusText(status), func(t *testing.T) {
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if status != 500 {
						w.Header().Set("Retry-After", "60")
					}
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"code":-1003,"msg":"limit"}`)
				}))
				defer server.Close()
				cfg := config.Defaults()
				cfg.HTTPClient.Retry.MaxAttempts = 2
				controller, err := upstream.New(cfg, upstream.SystemClock{})
				require.NoError(t, err)
				var events []upstream.Event
				transport, err := upstream.NewTransport(controller, scope, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, func(e upstream.Event) { events = append(events, e) })
				require.NoError(t, err)
				client, err := NewClient(scope, server.URL, transport)
				require.NoError(t, err)
				ctx, cancel, err := controller.Begin(t.Context(), scope, upstream.Tickers)
				require.NoError(t, err)
				defer cancel()
				path := "/api/v3/ticker/price"
				if scope == upstream.BinanceLinear {
					path = "/fapi/v2/ticker/price"
				}
				_, err = client.Fetch(ctx, path, nil)
				expected := int64(1)
				if status == 500 {
					expected = 2
					assert.ErrorIs(t, err, application.ErrUpstream)
				} else {
					assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
				}
				assert.Equal(t, expected, calls.Load())
				require.Len(t, events, int(expected))
				assert.Equal(t, status, events[0].Status)
				if status != 500 {
					assert.Equal(t, "60", events[0].Header.Get("Retry-After"))
				}
			})
		}
	}
}

func TestSDKCancellationReachesTransport(t *testing.T) {
	dispatched := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(dispatched)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	cfg := config.Defaults()
	controller, err := upstream.New(cfg, upstream.SystemClock{})
	require.NoError(t, err)
	transport, err := upstream.NewTransport(controller, upstream.BinanceSpot, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
	require.NoError(t, err)
	client, err := NewClient(upstream.BinanceSpot, server.URL, transport)
	require.NoError(t, err)
	ctx, cancel, err := controller.Begin(t.Context(), upstream.BinanceSpot, upstream.Tickers)
	require.NoError(t, err)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.Fetch(ctx, "/api/v3/ticker/price", nil)
		result <- err
	}()
	select {
	case <-dispatched:
	case err := <-result:
		require.FailNow(t, "request ended before dispatch", "%v", err)
	}
	cancel()
	assert.ErrorIs(t, <-result, context.Canceled)
	<-stopped
	assert.Equal(t, 1, controller.Attempts(ctx))
	_, err = client.Fetch(ctx, "/api/v3/ticker/price", nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, controller.Attempts(ctx))
}
