package upstream

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCatalogSelectsCeilingAndPreservesUsage(t *testing.T) {
	cases := []struct {
		name, yaml string
		next, want int
	}{
		{"increase without cap", "", 10000, 9000},
		{"decrease without cap", "", 4000, 3600},
		{"decrease below statistics cost", "", 1000, 900},
		{"decrease to zero rounded allowance", "", 1, 0},
		{"explicit cap", "upstream: {limits: {binance_spot: {windows: {request_weight_1m: {limit: 5000}}}}}", 10000, 4500},
		{"explicit default cap", "upstream: {limits: {binance_spot: {windows: {request_weight_1m: {limit: 6000}}}}}", 10000, 5400},
		{"configured 80", "upstream: {binance: {stop_threshold_percent: 80}}", 10000, 8000},
		{"configured 85", "upstream: {binance: {stop_threshold_percent: 85}}", 10000, 8500},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Load(strings.NewReader(tt.yaml), nil)
			require.NoError(t, err)
			c, err := New(cfg, SystemClock{})
			require.NoError(t, err)
			state := c.scopes[BinanceSpot]
			other := maps.Clone(c.scopes[BinanceLinear].windows)
			now := time.Now()
			state.history = []entry{{at: now, cost: cost{operation: MarketStats, weight: 80}}}
			require.NoError(t, c.updateCatalog(BinanceSpot, now, catalogWeight(6000)))

			require.NoError(t, c.updateCatalog(BinanceSpot, now.Add(time.Second), catalogWeight(tt.next)))

			assert.Equal(t, tt.want, state.windows["request_weight_1m"].limit)
			assert.Equal(t, tt.next, state.windows["request_weight_1m"].ceiling)
			assert.Equal(t, []entry{{at: now, cost: cost{operation: MarketStats, weight: 80}}}, state.history)
			assert.Equal(t, other, c.scopes[BinanceLinear].windows)
		})
	}
}

func TestInvalidCatalogPreservesWholeAcceptedState(t *testing.T) {
	cases := []struct{ name, rule string }{
		{"malformed JSON", `{"rateLimits":[`},
		{"missing rules", `{}`},
		{"unknown type", `{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":10000},{"rateLimitType":"OTHER"}]}`},
		{"invalid interval", `{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"WEEK","intervalNum":1,"limit":10000}]}`},
		{"duplicate", `{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":10000},{"rateLimitType":"REQUEST_WEIGHT","interval":"SECOND","intervalNum":60,"limit":6000}]}`},
		{"zero limit", string(catalogWeight(0))},
		{"negative limit", string(catalogWeight(-1))},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(config.Defaults(), SystemClock{})
			require.NoError(t, err)
			start := time.Now()
			require.NoError(t, c.updateCatalog(BinanceSpot, start, catalogWeight(4000)))
			before := c.Limits(BinanceSpot)
			updatedAt := c.scopes[BinanceSpot].catalogUpdatedAt

			err = c.updateCatalog(BinanceSpot, start.Add(2*time.Second), []byte(tt.rule))

			assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
			assert.Equal(t, before, c.Limits(BinanceSpot))
			assert.Equal(t, start, c.scopes[BinanceSpot].catalogStart)
			assert.Equal(t, updatedAt, c.scopes[BinanceSpot].catalogUpdatedAt)
			// A failed newer request does not make a valid in-flight response stale.
			require.NoError(t, c.updateCatalog(BinanceSpot, start.Add(time.Second), catalogWeight(10000)))
			assert.Equal(t, 9000, c.scopes[BinanceSpot].windows["request_weight_1m"].limit)
		})
	}
}

func TestReducedCatalogCanRecoverThroughNormalHTTPAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bodies := []string{string(catalogWeight(1000)), string(catalogWeight(10000))}
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			body := bodies[0]
			bodies = bodies[1:]
			return response(http.StatusOK, nil, body), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)

		_, err := send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo?showPermissionSets=false")
		require.NoError(t, err)
		assert.Equal(t, 900, c.scopes[BinanceSpot].windows["request_weight_1m"].limit)
		_, err = send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo?showPermissionSets=false")

		require.NoError(t, err)
		assert.Len(t, base.sent(), 2)
		assert.Equal(t, 9000, c.scopes[BinanceSpot].windows["request_weight_1m"].limit)
		require.Len(t, c.scopes[BinanceSpot].history, 2)
		assert.Equal(t, 20, c.scopes[BinanceSpot].history[0].cost.weight)
		assert.Equal(t, 20, c.scopes[BinanceSpot].history[1].cost.weight)
	})
}

func TestCatalogSourceAndMissingRules(t *testing.T) {
	c, err := New(config.Defaults(), SystemClock{})
	require.NoError(t, err)
	start := time.Now()
	before := c.Limits(BinanceLinear)
	for _, state := range before {
		assert.Equal(t, "bootstrap", state.Source)
		assert.True(t, state.UpdatedAt.IsZero())
	}
	require.NoError(t, c.updateCatalog(BinanceLinear, start, catalogWeight(4000)))
	require.NoError(t, c.updateCatalog(BinanceLinear, start.Add(time.Second), []byte(`{"rateLimits":[{"rateLimitType":"ORDERS"}]}`)))

	after := c.Limits(BinanceLinear)
	assert.Equal(t, before[0], after[0])
	assert.Equal(t, "funding_requests_5m", after[0].Name)
	assert.Equal(t, 500, after[0].ExchangeLimit)
	assert.Equal(t, "exchange_info", after[1].Source)
	assert.Equal(t, 4000, after[1].ExchangeLimit)
	assert.False(t, after[1].UpdatedAt.IsZero())
	after[1].ExchangeLimit = 1
	assert.Equal(t, 4000, c.Limits(BinanceLinear)[1].ExchangeLimit)
}

func catalogWeight(limit int) []byte {
	return []byte(fmt.Sprintf(`{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":%d}]}`, limit))
}

func TestCatalogReductionRejectsOnlyRequestsThatCannotFit(t *testing.T) {
	cases := []struct {
		name                              string
		scope                             Scope
		limit, stopLine, statsCost, share int
		catalogPath, statsPath, pricePath string
	}{
		{
			name: "spot", scope: BinanceSpot, limit: 1000, stopLine: 900, statsCost: 80, share: 45,
			catalogPath: "/api/v3/exchangeInfo?showPermissionSets=false",
			statsPath:   "/api/v3/ticker/24hr", pricePath: "/api/v3/ticker/price",
		},
		{
			name: "linear", scope: BinanceLinear, limit: 500, stopLine: 450, statsCost: 40, share: 22,
			catalogPath: "/fapi/v1/exchangeInfo",
			statsPath:   "/fapi/v1/ticker/24hr", pricePath: "/fapi/v2/ticker/price",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				base := &recorder{handle: func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/exchangeInfo") {
						return response(http.StatusOK, nil, string(catalogWeight(tt.limit))), nil
					}
					return response(http.StatusOK, nil, `[]`), nil
				}}
				c, transport := setup(t, config.Defaults(), tt.scope, base)
				_, err := send(begin(t, c, tt.scope, MarketStats), transport, tt.statsPath)
				require.NoError(t, err)
				previous := append([]entry(nil), c.scopes[tt.scope].history...)

				body, err := send(begin(t, c, tt.scope, Instruments), transport, tt.catalogPath)

				require.NoError(t, err)
				assert.Equal(t, string(catalogWeight(tt.limit)), string(body))
				state := c.scopes[tt.scope]
				assert.Equal(t, tt.limit, state.windows["request_weight_1m"].ceiling)
				assert.Equal(t, tt.stopLine, state.windows["request_weight_1m"].limit)
				require.Len(t, state.history, 2)
				assert.Equal(t, previous, state.history[:1])
				ctx := begin(t, c, tt.scope, MarketStats)
				beforeRejection := time.Now()

				_, err = send(ctx, transport, tt.statsPath)

				assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
				assert.ErrorContains(t, err, fmt.Sprintf("request cost %d exceeds allowance %d", tt.statsCost, tt.share))
				assert.Equal(t, beforeRejection, time.Now())
				assert.Zero(t, c.Attempts(ctx))
				assert.Len(t, base.sent(), 2)
				assert.Len(t, state.history, 2)

				_, err = send(begin(t, c, tt.scope, Tickers), transport, tt.pricePath)

				require.NoError(t, err)
				assert.Len(t, base.sent(), 3)
			})
		})
	}
}

func TestCatalogReductionKeepsSpentUsageConstrainingOtherOperations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &recorder{handle: func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/exchangeInfo") {
				return response(http.StatusOK, nil, string(catalogWeight(100))), nil
			}
			return response(http.StatusOK, nil, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		_, err := send(begin(t, c, BinanceSpot, MarketStats), transport, "/api/v3/ticker/24hr")
		require.NoError(t, err)

		_, err = send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo?showPermissionSets=false")

		require.NoError(t, err)
		assert.Equal(t, 90, c.scopes[BinanceSpot].windows["request_weight_1m"].limit)
		ctx, cancel := context.WithTimeout(begin(t, c, BinanceSpot, Tickers), time.Second)
		defer cancel()
		started := time.Now()

		_, err = send(ctx, transport, "/api/v3/ticker/price")

		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, started.Add(time.Second), time.Now())
		assert.Zero(t, c.Attempts(ctx))
		assert.Len(t, base.sent(), 2)
		require.Len(t, c.scopes[BinanceSpot].history, 2)
		assert.Equal(t, 80, c.scopes[BinanceSpot].history[0].cost.weight)
		assert.Equal(t, 20, c.scopes[BinanceSpot].history[1].cost.weight)
	})
}

func TestCatalogReductionRechecksWaitingRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &recorder{}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		_, err := send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/price")
		require.NoError(t, err)
		ctx := begin(t, c, BinanceSpot, MarketStats)
		done := make(chan error, 1)
		go func() {
			_, err := send(ctx, transport, "/api/v3/ticker/24hr")
			done <- err
		}()
		synctest.Wait()
		started := time.Now()

		require.NoError(t, c.updateCatalog(BinanceSpot, started, catalogWeight(1000)))

		assert.ErrorIs(t, <-done, application.ErrUpstreamUnavailable)
		assert.Equal(t, started, time.Now())
		assert.Zero(t, c.Attempts(ctx))
		assert.Len(t, base.sent(), 1)
	})
}

func TestCatalogReductionToZeroAllowanceDoesNotRestoreOldLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, nil, string(catalogWeight(1))), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)

		_, err := send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo?showPermissionSets=false")

		require.NoError(t, err)
		assert.Equal(t, 1, c.scopes[BinanceSpot].windows["request_weight_1m"].ceiling)
		assert.Zero(t, c.scopes[BinanceSpot].windows["request_weight_1m"].limit)
		time.Sleep(time.Minute)
		ctx := begin(t, c, BinanceSpot, Instruments)
		started := time.Now()

		_, err = send(ctx, transport, "/api/v3/exchangeInfo?showPermissionSets=false")

		assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
		assert.ErrorContains(t, err, "request cost 20 exceeds allowance 0")
		assert.Equal(t, started, time.Now())
		assert.Zero(t, c.Attempts(ctx))
		assert.Len(t, base.sent(), 1)
		assert.Zero(t, c.scopes[BinanceSpot].windows["request_weight_1m"].limit)
	})
}
