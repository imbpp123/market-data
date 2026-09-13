package upstream

import (
	"context"
	"io"
	"math"
	"net/http"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func currentUsage(c *Controller, scope Scope, name string) usageView {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.scopes[scope]
	return state.usage(state.windows[name], c.clock.Now())
}

func TestUsageIncludesOwnResponseAndUnmatchedReservations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reply := make(chan struct{})
		base := &recorder{handle: func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/api/v3/exchangeInfo" {
				return response(200, http.Header{"X-Mbx-Used-Weight-1m": {"100"}}, `{"rateLimits":[]}`), nil
			}
			<-reply
			return response(200, nil, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		_, err := send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")
		require.NoError(t, err)
		done := make(chan error, 2)
		for range 2 {
			ctx := begin(t, c, BinanceSpot, Tickers)
			go func() {
				_, err := send(ctx, transport, "/api/v3/ticker/price")
				done <- err
			}()
			time.Sleep(20 * time.Millisecond)
			synctest.Wait()
		}

		usage := currentUsage(c, BinanceSpot, "request_weight_1m")

		assert.Equal(t, 108, usage.common)
		assert.Equal(t, 28, usage.local)
		assert.Equal(t, 100, usage.observed)
		assert.Equal(t, 8, usage.reserved)
		assert.Equal(t, 20, usage.operations[Instruments])
		assert.Equal(t, 8, usage.operations[Tickers])
		assert.True(t, usage.uncertain)
		assert.True(t, usage.incompleteHistory)
		close(reply)
		require.NoError(t, <-done)
		require.NoError(t, <-done)
		assert.Equal(t, 108, currentUsage(c, BinanceSpot, "request_weight_1m").common)
	})
}

func TestSerialHeadersUseConservativeOverlapEstimate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		used := 0
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			used += 4
			return response(200, http.Header{"X-Mbx-Used-Weight-1m": {strconv.Itoa(used)}}, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		for range 3 {
			_, err := send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/price")
			require.NoError(t, err)
		}

		usage := currentUsage(c, BinanceSpot, "request_weight_1m")

		assert.Equal(t, 12, usage.local)
		assert.Equal(t, 12, usage.observed)
		// The counter may include both earlier attempts, but does not prove it.
		assert.Equal(t, 20, usage.common)
		assert.True(t, usage.uncertain)
	})
}

func TestReverseResponsesAndSmallerCounterKeepUsage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		firstReply := make(chan struct{})
		base := &recorder{handle: func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/api/v3/ticker/price" {
				<-firstReply
				return response(200, http.Header{"X-Mbx-Used-Weight-1m": {"4"}}, `[]`), nil
			}
			return response(200, http.Header{"X-Mbx-Used-Weight-1m": {"100"}}, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		done := make(chan error, 1)
		ctx := begin(t, c, BinanceSpot, Tickers)
		go func() {
			_, err := send(ctx, transport, "/api/v3/ticker/price")
			done <- err
		}()
		synctest.Wait()
		_, err := send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/bookTicker")
		require.NoError(t, err)
		assert.Equal(t, 104, currentUsage(c, BinanceSpot, "request_weight_1m").common)

		close(firstReply)
		require.NoError(t, <-done)

		usage := currentUsage(c, BinanceSpot, "request_weight_1m")
		assert.Equal(t, 104, usage.common)
		assert.Equal(t, 8, usage.local)
		assert.Zero(t, usage.reserved)
	})
}

func TestInvalidWeightHeadersKeepLocalUsage(t *testing.T) {
	cases := []struct {
		name    string
		headers http.Header
	}{
		{name: "missing"},
		{name: "negative", headers: http.Header{"X-Mbx-Used-Weight-1m": {"-1"}}},
		{name: "malformed", headers: http.Header{"X-Mbx-Used-Weight-1m": {"1.5"}}},
		{name: "overflow", headers: http.Header{"X-Mbx-Used-Weight-1m": {"999999999999999999999999"}}},
		{name: "conflicting values", headers: http.Header{"X-Mbx-Used-Weight-1m": {"100", "101"}}},
		{name: "conflicting case", headers: http.Header{"X-Mbx-Used-Weight-1m": {"100"}, "x-mbx-used-weight-1m": {"101"}}},
		{name: "conflicting list", headers: http.Header{"X-Mbx-Used-Weight-1m": {"100,101"}}},
		{name: "below own cost", headers: http.Header{"X-Mbx-Used-Weight-1m": {"3"}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				base := &recorder{handle: func(*http.Request) (*http.Response, error) {
					return response(200, tt.headers, `[]`), nil
				}}
				c, transport := setup(t, config.Defaults(), BinanceSpot, base)

				_, err := send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/price")

				require.NoError(t, err)
				usage := currentUsage(c, BinanceSpot, "request_weight_1m")
				assert.Equal(t, 4, usage.common)
				assert.Zero(t, usage.observed)
				assert.True(t, usage.uncertain)
			})
		})
	}
}

func TestWeightHeaderAcceptsEqualValuesAndSaturates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			value := strconv.Itoa(math.MaxInt)
			return response(200, http.Header{"X-Mbx-Used-Weight-1m": {value + ", " + value}, "x-mbx-used-weight-1m": {value}}, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		_, err := send(begin(t, c, BinanceSpot, Klines), transport, "/api/v3/klines?limit=1")
		require.NoError(t, err)

		_, err = send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/price")

		assert.ErrorIs(t, err, application.ErrServiceOverloaded)
		assert.Len(t, base.sent(), 1)
		assert.Equal(t, math.MaxInt, currentUsage(c, BinanceSpot, "request_weight_1m").common)
		assert.Equal(t, math.MaxInt, addUsage(math.MaxInt-1, 4))
	})
}

func TestObservationExpiresOneWindowAfterReceipt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			time.Sleep(2 * time.Second)
			return response(200, http.Header{"X-Mbx-Used-Weight-1m": {"100"}, "Date": {"Mon, 01 Jan 1990 00:00:00 GMT"}}, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		_, err := send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/price")
		require.NoError(t, err)
		time.Sleep(time.Until(start.Add(time.Minute)))
		assert.Equal(t, 100, currentUsage(c, BinanceSpot, "request_weight_1m").common)
		assert.Zero(t, currentUsage(c, BinanceSpot, "request_weight_1m").local)
		time.Sleep(2*time.Second - time.Nanosecond)
		assert.Equal(t, 100, currentUsage(c, BinanceSpot, "request_weight_1m").common)

		time.Sleep(time.Nanosecond)

		assert.Zero(t, currentUsage(c, BinanceSpot, "request_weight_1m").common)
	})
}

func TestCatalogResponseAccountsNewWeightWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			return response(200, http.Header{"X-Mbx-Used-Weight-5m": {"100"}}, `{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":5,"limit":6000}]}`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		time.Sleep(time.Minute)

		_, err := send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")

		require.NoError(t, err)
		usage := currentUsage(c, BinanceSpot, "weight_300000000000")
		assert.Equal(t, 100, usage.common)
		assert.Equal(t, 20, usage.local)
		assert.True(t, usage.incompleteHistory)
		assert.True(t, c.scopes[BinanceSpot].cooldown.IsZero())
	})
}

func TestPagesAndRetryPayOncePerAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			calls++
			if calls == 2 {
				return nil, io.ErrUnexpectedEOF
			}
			return response(200, nil, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		ctx := begin(t, c, BinanceSpot, Klines)
		for _, start := range []string{"1000", "2000"} {
			_, err := send(ctx, transport, "/api/v3/klines?limit=1&startTime="+start)
			require.NoError(t, err)
		}

		usage := currentUsage(c, BinanceSpot, "request_weight_1m")

		assert.Equal(t, 3, c.Attempts(ctx))
		assert.Len(t, base.sent(), 3)
		assert.Equal(t, 6, usage.common)
		assert.Equal(t, 6, usage.operations[Klines])
		assert.Equal(t, 3, currentUsage(c, BinanceSpot, "raw_requests_5m").common)
	})
}

func TestPostDispatchFailureKeepsUsage(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "cancellation", err: context.Canceled},
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "connection failure", err: io.ErrUnexpectedEOF},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := config.Defaults()
				cfg.HTTPClient.Retry.MaxAttempts = 1
				base := &recorder{handle: func(*http.Request) (*http.Response, error) { return nil, tt.err }}
				c, transport := setup(t, cfg, BinanceSpot, base)
				ctx := begin(t, c, BinanceSpot, Tickers)

				_, err := send(ctx, transport, "/api/v3/ticker/price")

				assert.ErrorIs(t, err, tt.err)
				assert.Equal(t, 1, c.Attempts(ctx))
				assert.Equal(t, 4, currentUsage(c, BinanceSpot, "request_weight_1m").common)
				assert.Zero(t, currentUsage(c, BinanceSpot, "request_weight_1m").reserved)
			})
		})
	}
}

func TestLongInflightUsagePreservesBybitBehavior(t *testing.T) {
	cases := []struct {
		name     string
		scope    Scope
		path     string
		window   string
		elapsed  time.Duration
		expected int
	}{
		{name: "Binance holds reservation", scope: BinanceLinear, path: "/fapi/v1/fundingInfo", window: "requests_1000000000", elapsed: 2 * time.Second, expected: 1},
		{name: "Bybit keeps sliding expiry", scope: Bybit, path: "/v5/market/instruments-info?category=spot", window: "http_requests_5s", elapsed: 6 * time.Second, expected: 2},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := smallBybitConfig()
				cfg.Upstream.LanesPerExchange.Instruments.HTTPSlots = 2
				reply := make(chan struct{})
				calls := 0
				base := &recorder{handle: func(*http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						<-reply
					}
					return response(200, nil, `{"retCode":0}`), nil
				}}
				c, transport := setup(t, cfg, tt.scope, base)
				if tt.scope == BinanceLinear {
					require.NoError(t, c.updateCatalog(tt.scope, time.Now(), []byte(`{"rateLimits":[{"rateLimitType":"RAW_REQUESTS","interval":"SECOND","intervalNum":1,"limit":25}]}`)))
				}
				first := begin(t, c, tt.scope, Instruments)
				done := make(chan error, 1)
				go func() {
					_, err := send(first, transport, tt.path)
					done <- err
				}()
				synctest.Wait()
				time.Sleep(tt.elapsed)
				second, cancel := context.WithCancel(begin(t, c, tt.scope, Instruments))
				defer cancel()
				waiting := make(chan error, 1)
				go func() {
					_, err := send(second, transport, tt.path)
					waiting <- err
				}()
				synctest.Wait()

				assert.Len(t, base.sent(), tt.expected)
				assert.Equal(t, 1, currentUsage(c, tt.scope, tt.window).common)
				if tt.scope == BinanceLinear {
					assert.Equal(t, 1, currentUsage(c, tt.scope, tt.window).reserved)
					cancel()
					assert.ErrorIs(t, <-waiting, context.Canceled)
				} else {
					require.NoError(t, <-waiting)
				}
				close(reply)
				require.NoError(t, <-done)
				if tt.scope == BinanceLinear {
					assert.Zero(t, currentUsage(c, tt.scope, tt.window).common)
				}
			})
		})
	}
}

func TestWeightRawFundingAndScopesStaySeparate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			return response(200, http.Header{"X-Mbx-Used-Weight-1m": {"100"}}, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceLinear, base)
		require.NoError(t, c.updateCatalog(BinanceLinear, time.Now(), []byte(`{"rateLimits":[{"rateLimitType":"RAW_REQUESTS","interval":"MINUTE","intervalNum":1,"limit":6000}]}`)))

		_, err := send(begin(t, c, BinanceLinear, Instruments), transport, "/fapi/v1/fundingInfo")

		require.NoError(t, err)
		assert.Equal(t, 100, currentUsage(c, BinanceLinear, "request_weight_1m").common)
		assert.Zero(t, currentUsage(c, BinanceLinear, "request_weight_1m").operations[Instruments])
		assert.Equal(t, 1, currentUsage(c, BinanceLinear, "requests_60000000000").common)
		assert.Equal(t, 1, currentUsage(c, BinanceLinear, "funding_requests_5m").common)
		assert.Zero(t, currentUsage(c, BinanceSpot, "request_weight_1m").common)
	})
}

func TestHeaderCannotReturnOperationShare(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			return response(200, http.Header{"X-Mbx-Used-Weight-1m": {"80"}}, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		for range 3 {
			_, err := send(begin(t, c, BinanceSpot, MarketStats), transport, "/api/v3/ticker/24hr")
			require.NoError(t, err)
		}

		_, err := send(begin(t, c, BinanceSpot, MarketStats), transport, "/api/v3/ticker/24hr")

		assert.ErrorIs(t, err, application.ErrServiceOverloaded)
		assert.Len(t, base.sent(), 3)
		assert.Equal(t, 240, currentUsage(c, BinanceSpot, "request_weight_1m").operations[MarketStats])
	})
}

func TestPruneRemovesOnlyUnusedEntries(t *testing.T) {
	now := time.Now()
	limit := window{unit: "weight", duration: time.Minute}
	state := &scopeState{windows: map[string]window{"minute": limit}, keepInflight: true, historySince: now.Add(-time.Hour)}
	for range 1000 {
		state.history = append(state.history, &entry{at: now.Add(-time.Hour), cost: cost{weight: 4}})
	}
	live := &entry{at: now, cost: cost{weight: 4}}
	inflight := &entry{at: now.Add(-time.Hour), cost: cost{weight: 4}, inflight: true}
	observed := &entry{at: now.Add(-time.Hour), cost: cost{weight: 4}, observations: map[time.Duration]usageObservation{time.Minute: {used: 100, expires: now.Add(time.Second)}}}
	state.history = append(state.history, live, inflight, observed)

	state.prune(now)

	assert.Equal(t, []*entry{live, inflight, observed}, state.history)
	assert.Equal(t, 108, state.usage(limit, now).common)
	state.prune(now.Add(time.Second))
	assert.Equal(t, []*entry{live, inflight}, state.history)
	assert.Equal(t, 8, state.usage(limit, now.Add(time.Second)).common)
	state.prune(now.Add(time.Minute))
	assert.Equal(t, []*entry{inflight}, state.history)
	inflight.inflight = false
	state.prune(now.Add(time.Minute))
	assert.Nil(t, state.history)
}

func TestCanceledReservationUsesAttemptIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := New(config.Defaults(), SystemClock{})
		require.NoError(t, err)
		state := c.scopes[BinanceSpot]
		ctx := begin(t, c, BinanceSpot, Tickers)
		operation, err := c.operation(ctx, BinanceSpot, Tickers)
		require.NoError(t, err)
		// Equal timestamps cannot identify an attempt. Roll back only its pointer.
		at := time.Now()
		first := &entry{at: at, cost: cost{weight: 4, operation: Tickers}, inflight: true, dispatched: true}
		second := &entry{at: at, cost: cost{weight: 4, operation: Tickers}, inflight: true}
		state.history = []*entry{first, second}
		operation.attempts = 2
		c.active = 2
		c.lanes[laneIndex(BinanceSpot, Tickers)].active = 2

		c.rollback(BinanceSpot, operation, second)

		assert.Equal(t, []*entry{first}, state.history)
		assert.Equal(t, 1, c.Attempts(ctx))
		assert.Equal(t, 1, c.active)
		assert.Equal(t, 4, currentUsage(c, BinanceSpot, "request_weight_1m").common)
	})
}
