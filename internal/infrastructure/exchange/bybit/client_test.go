package bybit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKPathsPreserveExactNumbersAndRequestLocalMetadata(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "linear", r.URL.Query().Get("category"))
		w.Header().Set("X-Test-Request", r.URL.Path)
		_, _ = io.WriteString(w, `{"retCode":0,"result":{"count":9007199254740993,"price":"0.1234567890123456789","nextPageCursor":"page2"}}`)
	}))
	defer server.Close()
	cfg := config.Defaults()
	controller, err := upstream.New(cfg, upstream.SystemClock{})
	require.NoError(t, err)
	transport, err := upstream.NewTransport(controller, upstream.Bybit, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
	require.NoError(t, err)
	client, err := NewClient(server.URL, transport)
	require.NoError(t, err)
	var group sync.WaitGroup
	for _, tt := range []struct {
		path   string
		kind   upstream.Operation
		params url.Values
	}{
		{"/v5/market/instruments-info", upstream.Instruments, url.Values{"category": {"linear"}, "cursor": {"page1"}}},
		{"/v5/market/tickers", upstream.Tickers, url.Values{"category": {"linear"}}},
		{"/v5/market/kline", upstream.Klines, url.Values{"category": {"linear"}, "limit": {"1000"}}},
	} {
		ctx, cancel, err := controller.Begin(t.Context(), upstream.Bybit, tt.kind)
		require.NoError(t, err)
		t.Cleanup(cancel)
		group.Go(func() {
			result, err := client.Fetch(ctx, tt.path, tt.params)
			if !assert.NoError(t, err) {
				return
			}
			var exact struct {
				Result struct {
					Count  int64  `json:"count"`
					Price  string `json:"price"`
					Cursor string `json:"nextPageCursor"`
				} `json:"result"`
			}
			if !assert.NoError(t, json.Unmarshal(result.Body, &exact)) {
				return
			}
			assert.Equal(t, int64(9007199254740993), exact.Result.Count)
			assert.Equal(t, "0.1234567890123456789", exact.Result.Price)
			assert.Equal(t, "page2", exact.Result.Cursor)
			assert.Equal(t, tt.path, result.Header.Get("X-Test-Request"))
			assert.Equal(t, 1, controller.Attempts(ctx))
		})
	}
	group.Wait()
	assert.Equal(t, int64(3), calls.Load())
}

func TestSDKRejectsRateCodeOnHTTP200(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-Bapi-Limit-Reset-Timestamp", "invalid")
		_, _ = io.WriteString(w, `{"retCode":10006,"retMsg":"Too many visits","result":{}}`)
	}))
	defer server.Close()
	cfg := config.Defaults()
	controller, err := upstream.New(cfg, upstream.SystemClock{})
	require.NoError(t, err)
	var events []upstream.Event
	transport, err := upstream.NewTransport(controller, upstream.Bybit, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, func(e upstream.Event) { events = append(events, e) })
	require.NoError(t, err)
	client, err := NewClient(server.URL, transport)
	require.NoError(t, err)
	ctx, cancel, err := controller.Begin(t.Context(), upstream.Bybit, upstream.Tickers)
	require.NoError(t, err)
	defer cancel()
	result, err := client.Fetch(ctx, "/v5/market/tickers", url.Values{"category": {"spot"}})
	assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
	assert.Empty(t, result.Body)
	assert.Equal(t, int64(1), calls.Load())
	require.Len(t, events, 1)
	assert.Equal(t, 200, events[0].Status)
	assert.Equal(t, int64(10006), events[0].Code)
	assert.Equal(t, "invalid", events[0].Header.Get("X-Bapi-Limit-Reset-Timestamp"))
}

func TestSDKCancellationAndPreDispatchValidation(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	cfg := config.Defaults()
	controller, err := upstream.New(cfg, upstream.SystemClock{})
	require.NoError(t, err)
	transport, err := upstream.NewTransport(controller, upstream.Bybit, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
	require.NoError(t, err)
	client, err := NewClient(server.URL, transport)
	require.NoError(t, err)
	ctx, cancel, err := controller.Begin(t.Context(), upstream.Bybit, upstream.Tickers)
	require.NoError(t, err)
	defer cancel()
	_, err = client.Fetch(ctx, "/unknown", nil)
	assert.ErrorIs(t, err, application.ErrUnsupportedOperation)
	_, err = client.Fetch(ctx, "/v5/market/tickers", url.Values{"category": {"spot", "linear"}})
	assert.ErrorIs(t, err, application.ErrInvalidParameter)
	result := make(chan error, 1)
	go func() {
		_, err := client.Fetch(ctx, "/v5/market/tickers", url.Values{"category": {"spot"}})
		result <- err
	}()
	select {
	case <-started:
	case err := <-result:
		require.FailNow(t, "request ended before dispatch", "%v", err)
	}
	cancel()
	assert.ErrorIs(t, <-result, context.Canceled)
	<-stopped
	assert.Equal(t, 1, controller.Attempts(ctx))
	_, err = client.Fetch(ctx, "/v5/market/tickers", url.Values{"category": {"spot"}})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, controller.Attempts(ctx))
}
