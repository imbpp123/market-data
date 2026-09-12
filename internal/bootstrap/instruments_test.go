package bootstrap

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type instrumentTransport func(*http.Request) (*http.Response, error)

func (f instrumentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInstrumentWorkersKeepExchangeFailuresIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Exchanges.Binance.Markets = []string{"spot"}
		cfg.Exchanges.Bybit.Markets = []string{"spot"}
		cfg.Exchanges.Bybit.Instruments.RefreshInterval = time.Minute
		state, err := newLocalState(1000, time.Now)
		require.NoError(t, err)
		clock := upstream.SystemClock{}
		jitter := func(time.Duration) time.Duration { return 0 }
		transport := instrumentTransport(func(r *http.Request) (*http.Response, error) {
			status, body := 200, `{"retCode":0,"result":{"category":"spot","list":[]}}`
			if r.URL.Host == "api.binance.com" {
				status, body = 400, `{"code":-1,"msg":"failure"}`
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})
		state.exchanges, err = newExchangeClients(cfg, transport, clock, jitter, nil)
		require.NoError(t, err)
		workers, err := state.instrumentWorkers(cfg, testLogger(), clock, jitter)
		require.NoError(t, err)
		require.Len(t, workers, 2)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, len(workers))
		for _, worker := range workers {
			go func() { done <- worker(ctx) }()
		}
		synctest.Wait()
		bybitScope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
		binanceScope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}

		ready, err := state.instruments.HasSnapshot(t.Context(), bybitScope)

		require.NoError(t, err)
		assert.True(t, ready)
		ready, err = state.instruments.HasSnapshot(t.Context(), binanceScope)
		require.NoError(t, err)
		assert.False(t, ready)
		stats := state.instrumentMetrics.Snapshot()
		assert.Equal(t, uint64(1), stats[bybitScope].RefreshTotal)
		assert.Equal(t, uint64(1), stats[binanceScope].RefreshErrorsTotal)
		time.Sleep(time.Minute)
		synctest.Wait()
		stats = state.instrumentMetrics.Snapshot()
		assert.Equal(t, uint64(2), stats[bybitScope].RefreshTotal)
		assert.Equal(t, uint64(1), stats[binanceScope].RefreshErrorsTotal)
		cancel()
		for range workers {
			assert.ErrorIs(t, <-done, context.Canceled)
		}
	})
}

func TestServeExposesInstrumentReadinessSeparately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		state, err := newLocalState(1000, time.Now)
		require.NoError(t, err)
		listener := newPipeListener()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- state.serve(ctx, cfg, testLogger(), listener) }()
		defer func() { cancel(); require.NoError(t, <-done) }()
		synctest.Wait()
		transport := &http.Transport{DialContext: listener.dial}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport}
		request, err := http.NewRequestWithContext(t.Context(), "GET", "http://local/api/v1/instruments", nil)
		require.NoError(t, err)

		response, err := client.Do(request)

		require.NoError(t, err)
		defer func() { _ = response.Body.Close() }()
		assert.Equal(t, 503, response.StatusCode)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "data_not_ready")
		assert.True(t, state.ready.Load())
	})
}

func TestEnabledScopesExcludeDisabledExchanges(t *testing.T) {
	cfg := config.Defaults()
	cfg.Exchanges.Binance.Enabled = false
	cfg.Exchanges.Bybit.Markets = []string{"linear"}

	scopes := enabledScopes(cfg)

	assert.Equal(t, []application.Scope{{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}}, scopes)
	reader := instrument.NewReader(nil, scopes)
	_, err := reader.List(t.Context(), instrument.Query{Exchange: "binance"})
	assert.ErrorIs(t, err, application.ErrInvalidFilter)
}

func TestInstrumentProviderPublishesSelectedScope(t *testing.T) {
	cases := []struct {
		name          string
		scope         application.Scope
		upstreamScope upstream.Scope
		path          string
		catalog       string
		wantStep      string
		wantFunding   time.Duration
	}{
		{"Binance spot", application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}, upstream.BinanceSpot, "/api/v3/exchangeInfo", `{"rateLimits":[],"symbols":[{"symbol":"ABCUSDT","baseAsset":"ABC","quoteAsset":"USDT","status":"TRADING","filters":[{"filterType":"PRICE_FILTER","tickSize":"0.1"},{"filterType":"LOT_SIZE","stepSize":"0.01"}]}]}`, "0.01", 0},
		{"Binance linear", application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}, upstream.BinanceLinear, "/fapi/v1/exchangeInfo", `{"rateLimits":[],"symbols":[{"symbol":"ABCUSDT","baseAsset":"ABC","quoteAsset":"USDT","status":"TRADING","contractType":"PERPETUAL","filters":[{"filterType":"PRICE_FILTER","tickSize":"0.1"},{"filterType":"LOT_SIZE","stepSize":"0.001"}]}]}`, "0.001", 4 * time.Hour},
		{"Bybit spot", application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}, upstream.Bybit, "/v5/market/instruments-info", `{"retCode":0,"result":{"category":"spot","list":[{"symbol":"ABCUSDT","baseCoin":"ABC","quoteCoin":"USDT","status":"Trading","priceFilter":{"tickSize":"0.1"},"lotSizeFilter":{"basePrecision":"0.0001","qtyStep":"99"}}]}}`, "0.0001", 0},
		{"Bybit linear", application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}, upstream.Bybit, "/v5/market/instruments-info", `{"retCode":0,"result":{"category":"linear","list":[{"symbol":"ABCUSDT","baseCoin":"ABC","quoteCoin":"USDT","status":"Trading","contractType":"LinearPerpetual","fundingInterval":480,"priceFilter":{"tickSize":"0.1"},"lotSizeFilter":{"qtyStep":"0.00001","basePrecision":"99"}}]}}`, "0.00001", 8 * time.Hour},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := config.Defaults()
				cfg.Exchanges.Binance.Enabled = tt.scope.Exchange == domain.ExchangeBinance
				cfg.Exchanges.Bybit.Enabled = tt.scope.Exchange == domain.ExchangeBybit
				cfg.Exchanges.Binance.Markets = []string{string(tt.scope.Market)}
				cfg.Exchanges.Bybit.Markets = []string{string(tt.scope.Market)}
				state, err := newLocalState(1000, time.Now)
				require.NoError(t, err)
				transport := instrumentTransport(func(r *http.Request) (*http.Response, error) {
					body := tt.catalog
					if r.URL.Path == "/fapi/v1/fundingInfo" {
						assert.Equal(t, domain.MarketLinear, tt.scope.Market)
						body = `[{"symbol":"ABCUSDT","fundingIntervalHours":4}]`
					} else {
						assert.Equal(t, tt.path, r.URL.Path)
					}
					if tt.scope.Exchange == domain.ExchangeBybit {
						assert.Equal(t, string(tt.scope.Market), r.URL.Query().Get("category"))
						if r.URL.Query().Get("status") == "PreLaunch" {
							body = `{"retCode":0,"result":{"category":"linear","list":[]}}`
						}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})
				state.exchanges, err = newExchangeClients(cfg, transport, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
				require.NoError(t, err)
				provider, err := state.instrumentProvider(tt.scope, testLogger())
				require.NoError(t, err)
				refresh := instrument.NewRefresher(provider, state.instruments, time.Now, nil)
				ctx, cancel, err := state.exchanges.admission.Begin(t.Context(), tt.upstreamScope, upstream.Instruments)
				require.NoError(t, err)
				defer cancel()

				require.NoError(t, refresh.Refresh(ctx))

				rows, err := state.instruments.List(t.Context(), instrument.Filter{SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{tt.scope}}})
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, tt.scope.Exchange, rows[0].Exchange)
				assert.Equal(t, tt.scope.Market, rows[0].Market)
				assert.Equal(t, "ABCUSDT", rows[0].Symbol)
				assert.Equal(t, tt.wantStep, rows[0].QtyStep.String())
				if tt.wantFunding == 0 {
					assert.Nil(t, rows[0].FundingInterval)
				} else {
					require.NotNil(t, rows[0].FundingInterval)
					assert.Equal(t, tt.wantFunding, *rows[0].FundingInterval)
				}
				assert.False(t, rows[0].UpdatedAt.IsZero())
			})
		})
	}
}
