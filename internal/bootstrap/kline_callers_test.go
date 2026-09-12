package bootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	httptransport "market-data/internal/transport/http"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockedKlineWriter struct {
	*httptest.ResponseRecorder
	started    chan struct{}
	release    chan struct{}
	once       sync.Once
	writeError error
}

func (w *blockedKlineWriter) Write(body []byte) (int, error) {
	close(w.started)
	<-w.release
	if w.writeError != nil {
		return 0, w.writeError
	}
	return w.ResponseRecorder.Write(body)
}

func (w *blockedKlineWriter) unblock() {
	w.once.Do(func() { close(w.release) })
}

func TestKlineCallerLimitIncludesResponseWrite(t *testing.T) {
	cases := []struct {
		name          string
		writeError    error
		cancelCaller  bool
		missingSymbol bool
	}{
		{name: "successful write"},
		{name: "failed write", writeError: io.ErrClosedPipe},
		{name: "canceled caller", writeError: context.Canceled, cancelCaller: true},
		{name: "error response", missingSymbol: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				handler, scope, end := newWarmKlineHandler(t)
				address := candleURL(scope, end.Add(-time.Minute), end)
				firstAddress := address
				if tc.missingSymbol {
					firstAddress = strings.Replace(address, "BTCUSDT", "UNKNOWN", 1)
				}
				caller, cancel := context.WithCancel(t.Context())
				defer cancel()
				blocked := &blockedKlineWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{}), writeError: tc.writeError}
				done := make(chan struct{})
				go func() {
					defer close(done)
					handler.ServeHTTP(blocked, httptest.NewRequestWithContext(caller, "GET", firstAddress, nil))
				}()
				defer func() {
					blocked.unblock()
					<-done
				}()
				<-blocked.started
				if tc.cancelCaller {
					cancel()
				}
				second := httptest.NewRecorder()

				handler.ServeHTTP(second, httptest.NewRequestWithContext(t.Context(), "GET", address, nil))

				assert.Equal(t, 503, second.Code)
				assert.Contains(t, second.Body.String(), "service_overloaded")
				assert.NotContains(t, second.Body.String(), `"data"`)

				blocked.unblock()
				<-done
				third := httptest.NewRecorder()
				handler.ServeHTTP(third, httptest.NewRequestWithContext(t.Context(), "GET", address, nil))
				assert.Equal(t, 200, third.Code, "finished responses must release capacity")
				assert.Contains(t, third.Body.String(), `"symbol":"BTCUSDT"`)
			})
		})
	}
}

func newWarmKlineHandler(t *testing.T) (http.Handler, application.Scope, time.Time) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Klines.MaxCallers = 1
	state, err := newLocalState(1000, time.Now)
	require.NoError(t, err)
	state.exchanges, err = newExchangeClients(cfg, noExchangeCalls{t}, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
	require.NoError(t, err)
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	require.NoError(t, state.instruments.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}))
	end := time.Now().UTC().Truncate(time.Minute)
	from := end.Add(-time.Minute)
	require.NoError(t, state.klines.UpsertMany(t.Context(), []kline.Stored{{
		Candle:           domain.Kline{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT", Interval: domain.Timeframe1m, OpenTime: from, CloseTime: end, FetchedAt: end},
		RequestStartedAt: end,
	}}))
	service, err := state.klineService(t.Context(), cfg, time.Now)
	require.NoError(t, err)
	handler := httptransport.NewKlinesHandler(service, cfg.Klines.RequestTimeout, cfg.Klines.MaxCallers)
	return handler, scope, end
}

func TestKlineValidationPrecedesCallerLimit(t *testing.T) {
	cases := []struct {
		name        string
		fromMinutes int
		toMinutes   int
		expired     bool
		status      int
		code        string
	}{
		{"reversed range", 1, 0, false, 400, "invalid_range"},
		{"too many slots", -1001, 0, false, 400, "request_too_large"},
		{"expired range", -1001, -1000, false, 400, "range_out_of_retention"},
		{"caller deadline", -1, 0, true, 504, "request_timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				handler, scope, end := newWarmKlineHandler(t)
				blocked := &blockedKlineWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
				done := make(chan struct{})
				go func() {
					defer close(done)
					handler.ServeHTTP(blocked, httptest.NewRequestWithContext(t.Context(), "GET", candleURL(scope, end.Add(-time.Minute), end), nil))
				}()
				defer func() {
					blocked.unblock()
					<-done
				}()
				<-blocked.started
				ctx := t.Context()
				if tc.expired {
					var stop context.CancelFunc
					ctx, stop = context.WithDeadline(ctx, time.Now().Add(-time.Second))
					defer stop()
				}
				response := httptest.NewRecorder()
				address := candleURL(scope, end.Add(time.Duration(tc.fromMinutes)*time.Minute), end.Add(time.Duration(tc.toMinutes)*time.Minute))

				handler.ServeHTTP(response, httptest.NewRequestWithContext(ctx, "GET", address, nil))

				assert.Equal(t, tc.status, response.Code)
				assert.Contains(t, response.Body.String(), tc.code)
			})
		})
	}
}
