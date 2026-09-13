package upstream

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBinanceCurrentThresholdAllowsCrossing(t *testing.T) {
	cases := []struct {
		used     int
		accepted bool
		total    int
	}{
		{used: 5398, accepted: true, total: 5402},
		{used: 5400, accepted: true, total: 5404},
		{used: 5401, total: 5401},
	}
	for _, tt := range cases {
		t.Run(strconv.Itoa(tt.used), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				base := &recorder{handle: func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/api/v3/klines" {
						return response(200, http.Header{"X-Mbx-Used-Weight-1m": {strconv.Itoa(tt.used)}}, `[]`), nil
					}
					return response(200, nil, `[]`), nil
				}}
				c, transport := setup(t, config.Defaults(), BinanceSpot, base)
				_, err := send(begin(t, c, BinanceSpot, Klines), transport, "/api/v3/klines?limit=1")
				require.NoError(t, err)
				time.Sleep(20 * time.Millisecond)
				ctx := begin(t, c, BinanceSpot, Tickers)
				started := time.Now()

				_, err = send(ctx, transport, "/api/v3/ticker/price")

				if tt.accepted {
					require.NoError(t, err)
					assert.Equal(t, 1, c.Attempts(ctx))
				} else {
					require.ErrorIs(t, err, application.ErrServiceOverloaded)
					assert.Zero(t, c.Attempts(ctx))
				}
				assert.Equal(t, started, time.Now())
				assert.Equal(t, tt.total, currentUsage(c, BinanceSpot, "request_weight_1m").common)
				attempts := c.Attempts(ctx)
				_, err = send(ctx, transport, "/api/v3/ticker/bookTicker")
				require.ErrorIs(t, err, application.ErrServiceOverloaded)
				assert.Equal(t, attempts, c.Attempts(ctx))
				assert.Len(t, base.sent(), 1+attempts)
				assert.Zero(t, c.active)
				assert.Zero(t, c.queued)
			})
		})
	}
}

func TestBinanceConcurrentCallersReserveOnlyOneCrossing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &recorder{handle: func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/api/v3/klines" {
				return response(200, http.Header{"X-Mbx-Used-Weight-1m": {"5400"}}, `[]`), nil
			}
			return response(200, nil, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		_, err := send(begin(t, c, BinanceSpot, Klines), transport, "/api/v3/klines?limit=1")
		require.NoError(t, err)
		time.Sleep(20 * time.Millisecond)
		var group sync.WaitGroup
		results := make(chan error, 2)
		for range 2 {
			ctx := begin(t, c, BinanceSpot, Tickers)
			group.Go(func() {
				_, err := send(ctx, transport, "/api/v3/ticker/price")
				results <- err
			})
		}
		group.Wait()
		close(results)
		accepted := 0
		for err := range results {
			if err == nil {
				accepted++
			} else {
				assert.ErrorIs(t, err, application.ErrServiceOverloaded)
			}
		}
		assert.Equal(t, 1, accepted)
		assert.Len(t, base.sent(), 2)
		assert.Equal(t, 5404, currentUsage(c, BinanceSpot, "request_weight_1m").common)
	})
}

func TestBinanceOperationShareIsStrict(t *testing.T) {
	cases := []struct {
		used     int
		accepted bool
	}{
		{used: 250, accepted: true},
		{used: 251},
	}
	for _, tt := range cases {
		t.Run(strconv.Itoa(tt.used), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, transport := setup(t, config.Defaults(), BinanceSpot, &recorder{})
				// Completed local instrument costs use only their own 270-unit share.
				c.scopes[BinanceSpot].history = []*entry{{at: time.Now(), cost: cost{operation: Instruments, weight: tt.used}, dispatched: true}}
				ctx := begin(t, c, BinanceSpot, Instruments)
				started := time.Now()

				_, err := send(ctx, transport, "/api/v3/exchangeInfo")

				if tt.accepted {
					require.NoError(t, err)
					assert.Equal(t, 270, currentUsage(c, BinanceSpot, "request_weight_1m").operations[Instruments])
				} else {
					require.ErrorIs(t, err, application.ErrServiceOverloaded)
					deferred := application.DeferredRefresh(err)
					require.NotNil(t, deferred)
					assert.Equal(t, "operation_share", deferred.Reason())
					assert.Equal(t, 251, currentUsage(c, BinanceSpot, "request_weight_1m").local)
					assert.Zero(t, c.Attempts(ctx))
				}
				assert.Equal(t, started, time.Now())
			})
		})
	}
}

func TestBudgetRejectionPrecedesQueueSlotsPacingAndCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &recorder{}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		start := time.Now()
		s := c.scopes[BinanceSpot]
		s.history = []*entry{{at: start, cost: cost{operation: Klines, weight: 2}, dispatched: true, observations: map[time.Duration]usageObservation{time.Minute: {used: 5401, expires: start.Add(time.Minute)}}}}
		s.next = start.Add(2 * time.Second)
		c.Cooldown(BinanceSpot, start.Add(2*time.Minute))
		// An unavailable lane must not hide the budget reason from the worker.
		c.lanes[Tickers].capacity = 0
		c.active = c.settings.MaxHTTPInflight
		ctx := begin(t, c, BinanceSpot, Tickers)

		_, err := send(ctx, transport, "/api/v3/ticker/price")

		require.ErrorIs(t, err, application.ErrServiceOverloaded)
		deferred := application.DeferredRefresh(err)
		require.NotNil(t, deferred)
		assert.Equal(t, "common_threshold", deferred.Reason())
		assert.Equal(t, start.Add(2*time.Minute), deferred.NextEligible())
		assert.Equal(t, start, time.Now())
		assert.Zero(t, c.Attempts(ctx))
		assert.Empty(t, base.sent())
		assert.Len(t, s.history, 1)
		assert.Zero(t, c.queued)
		assert.Equal(t, c.settings.MaxHTTPInflight, c.active)
	})
}

func TestBudgetRejectionOnRetryDoesNotSpendAttemptOrBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			return response(500, http.Header{"X-Mbx-Used-Weight-1m": {"5401"}}, `{}`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		ctx := begin(t, c, BinanceSpot, Tickers)
		start := time.Now()

		_, err := send(ctx, transport, "/api/v3/ticker/price")

		require.ErrorIs(t, err, application.ErrServiceOverloaded)
		assert.Equal(t, start, time.Now())
		assert.Len(t, base.sent(), 1)
		assert.Equal(t, 1, c.Attempts(ctx))
		assert.Equal(t, 5401, currentUsage(c, BinanceSpot, "request_weight_1m").common)
	})
}

func TestBudgetDeferralRechecksWindowsWithoutMovingExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, transport := setup(t, config.Defaults(), BinanceSpot, &recorder{})
		start := time.Now()
		s := c.scopes[BinanceSpot]
		// The earliest local expiry does not clear the later observed constraint.
		s.history = []*entry{{at: start.Add(-30 * time.Second), cost: cost{operation: Klines, weight: 2}, dispatched: true, observations: map[time.Duration]usageObservation{time.Minute: {used: 5401, expires: start.Add(time.Minute)}}}}
		_, err := send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/price")
		deferred := application.DeferredRefresh(err)
		require.NotNil(t, deferred)
		assert.Equal(t, start.Add(time.Minute), deferred.NextEligible())
		time.Sleep(10 * time.Second)
		_, err = send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/price")
		repeated := application.DeferredRefresh(err)
		require.NotNil(t, repeated)
		assert.Equal(t, deferred.NextEligible(), repeated.NextEligible())
		// A new active window extends recovery; no response is needed to clear it.
		require.NoError(t, c.updateCatalog(BinanceSpot, time.Now(), []byte(`{"rateLimits":[{"rateLimitType":"RAW_REQUESTS","interval":"MINUTE","intervalNum":2,"limit":25}]}`)))
		s.history = append(s.history, &entry{at: start, cost: cost{operation: Instruments, weight: 20}, dispatched: true})
		_, err = send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")
		deferred = application.DeferredRefresh(err)
		require.NotNil(t, deferred)
		assert.Equal(t, start.Add(2*time.Minute), deferred.NextEligible())

		require.NoError(t, deferred.Wait(t.Context()))

		assert.Equal(t, start.Add(2*time.Minute), time.Now())
		_, err = send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")
		require.NoError(t, err)
	})
}

func TestBudgetDeferralWaitsForInflightCompletionAndCanCancel(t *testing.T) {
	cases := []struct {
		name   string
		cancel bool
	}{
		{name: "completion resumes"},
		{name: "shutdown cancels", cancel: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := config.Defaults()
				cfg.Upstream.LanesPerExchange.Instruments.HTTPSlots = 2
				reply := make(chan struct{})
				base := &recorder{handle: func(*http.Request) (*http.Response, error) {
					<-reply
					return response(200, nil, `{}`), nil
				}}
				c, transport := setup(t, cfg, BinanceLinear, base)
				require.NoError(t, c.updateCatalog(BinanceLinear, time.Now(), []byte(`{"rateLimits":[{"rateLimitType":"RAW_REQUESTS","interval":"SECOND","intervalNum":1,"limit":25}]}`)))
				first := begin(t, c, BinanceLinear, Instruments)
				done := make(chan error, 1)
				go func() {
					_, err := send(first, transport, "/fapi/v1/fundingInfo")
					done <- err
				}()
				synctest.Wait()
				time.Sleep(2 * time.Second)
				_, err := send(begin(t, c, BinanceLinear, Instruments), transport, "/fapi/v1/fundingInfo")
				deferred := application.DeferredRefresh(err)
				require.NotNil(t, deferred)
				assert.True(t, deferred.NextEligible().IsZero())
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				resumed := make(chan error, 1)
				go func() { resumed <- deferred.Wait(ctx) }()
				synctest.Wait()
				assert.Len(t, base.sent(), 1)
				select {
				case <-resumed:
					require.FailNow(t, "in-flight budget must remain blocked")
				default:
				}
				if tt.cancel {
					cancel()
					assert.ErrorIs(t, <-resumed, context.Canceled)
				}
				close(reply)
				require.NoError(t, <-done)
				if !tt.cancel {
					require.NoError(t, <-resumed)
				}
				assert.Zero(t, currentUsage(c, BinanceLinear, "requests_1000000000").common)
			})
		})
	}
}
