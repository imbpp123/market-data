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

func smallBybitConfig() config.Config {
	cfg := config.Defaults()
	w := cfg.Upstream.Limits.Bybit.Windows["http_requests_5s"]
	w.Limit = 25 // 20 common; 13 shared ticker, 6 kline, 1 instrument.
	cfg.Upstream.Limits.Bybit.Windows["http_requests_5s"] = w
	return cfg
}

func TestReservedSharesAndSlidingBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &recorder{}
		c, transport := setup(t, smallBybitConfig(), Bybit, recorder)
		start := time.Now()
		for range 13 {
			_, err := send(begin(t, c, Bybit, Tickers), transport, "/v5/market/tickers?category=spot")
			require.NoError(t, err)
		}
		waiting := make(chan error, 1)
		ctx := begin(t, c, Bybit, Tickers)
		go func() {
			_, err := send(ctx, transport, "/v5/market/tickers?category=linear")
			waiting <- err
		}()
		synctest.Wait()
		require.Len(t, recorder.sent(), 13)
		_, err := send(begin(t, c, Bybit, Instruments), transport, "/v5/market/instruments-info?category=linear")
		require.NoError(t, err)
		_, err = send(begin(t, c, Bybit, Klines), transport, "/v5/market/kline?category=spot&limit=1000")
		require.NoError(t, err)
		require.Len(t, recorder.sent(), 15)
		time.Sleep(time.Until(start.Add(5*time.Second - time.Nanosecond)))
		synctest.Wait()
		assert.Len(t, recorder.sent(), 15)
		time.Sleep(time.Nanosecond)
		require.NoError(t, <-waiting)
		sent := recorder.sent()
		require.Len(t, sent, 16)
		assert.Equal(t, start.Add(5*time.Second), sent[15].at)
	})
}

func TestFIFOAndRoundRobinAcrossReadyLanes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &recorder{}
		c, transport := setup(t, config.Defaults(), Bybit, recorder)
		start := time.Now()
		c.Cooldown(Bybit, start.Add(time.Second))
		var group sync.WaitGroup
		paths := []string{"/v5/market/tickers?category=spot&symbol=first", "/v5/market/tickers?category=spot&symbol=second", "/v5/market/instruments-info?category=linear", "/v5/market/kline?category=spot&limit=1"}
		kinds := []Operation{Tickers, Tickers, Instruments, Klines}
		for i, path := range paths {
			ctx := begin(t, c, Bybit, kinds[i])
			group.Go(func() {
				_, err := send(ctx, transport, path)
				assert.NoError(t, err)
			})
			synctest.Wait()
		}
		assert.Empty(t, recorder.sent())
		group.Wait()
		sent := recorder.sent()
		require.Len(t, sent, 4)
		assert.Equal(t, []string{paths[0], paths[3], paths[2], paths[1]}, []string{sent[0].path, sent[1].path, sent[2].path, sent[3].path})
		for i, row := range sent {
			assert.Equal(t, start.Add(time.Second+time.Duration(i)*10*time.Millisecond), row.at)
		}
	})
}

func TestQueueOverflowCancellationAndNoSlotWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Upstream.LanesPerExchange.Tickers.Waiters = 1
		recorder := &recorder{}
		c, transport := setup(t, cfg, BinanceSpot, recorder)
		c.Cooldown(BinanceSpot, time.Now().Add(5*time.Second))
		ctx, cancel := context.WithCancel(begin(t, c, BinanceSpot, Tickers))
		result := make(chan error, 1)
		go func() {
			_, err := send(ctx, transport, "/api/v3/ticker/price")
			result <- err
		}()
		synctest.Wait()
		assert.Zero(t, c.active)
		_, err := send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/bookTicker")
		assert.ErrorIs(t, err, application.ErrServiceOverloaded)
		cancel()
		assert.ErrorIs(t, <-result, context.Canceled)
		assert.Zero(t, c.Attempts(ctx))
		assert.Empty(t, recorder.sent())
		assert.Zero(t, c.queued)
		// The same exchange's other API scope has its own budget and cooldown.
		linear, err := NewTransport(c, BinanceLinear, recorder, cfg, func(d time.Duration) time.Duration { return d }, nil)
		require.NoError(t, err)
		_, err = send(begin(t, c, BinanceLinear, Tickers), linear, "/fapi/v2/ticker/price")
		require.NoError(t, err)
		assert.Len(t, recorder.sent(), 1)
	})
}

func TestConcurrencySlotsAndCanceledQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Upstream.LanesPerExchange.Tickers.HTTPSlots = 1
		release := make(chan struct{})
		recorder := &recorder{handle: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/v5/market/tickers" {
				select {
				case <-release:
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			}
			return response(200, nil, `{"retCode":0}`), nil
		}}
		c, transport := setup(t, cfg, Bybit, recorder)
		first := begin(t, c, Bybit, Tickers)
		done := make(chan error, 1)
		go func() {
			_, err := send(first, transport, "/v5/market/tickers?category=spot")
			done <- err
		}()
		synctest.Wait()
		ctx, cancel := context.WithCancel(begin(t, c, Bybit, Tickers))
		waiting := make(chan error, 1)
		go func() {
			_, err := send(ctx, transport, "/v5/market/tickers?category=linear")
			waiting <- err
		}()
		synctest.Wait()
		_, err := send(begin(t, c, Bybit, Instruments), transport, "/v5/market/instruments-info?category=spot")
		require.NoError(t, err)
		assert.Len(t, recorder.sent(), 2)
		cancel()
		assert.ErrorIs(t, <-waiting, context.Canceled)
		assert.Zero(t, c.Attempts(ctx))
		close(release)
		require.NoError(t, <-done)
		assert.Zero(t, c.active)
	})
}

func TestAllWindowsAreConsumedAtomically(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		recorder := &recorder{}
		c, transport := setup(t, cfg, BinanceLinear, recorder)
		start := time.Now()
		// A discovered short raw-request window supplements weight and family limits.
		require.NoError(t, c.updateCatalog(BinanceLinear, start, []byte(`{"rateLimits":[{"rateLimitType":"RAW_REQUESTS","interval":"SECOND","intervalNum":1,"limit":25}]}`)))
		time.Sleep(time.Second)
		ctx := begin(t, c, BinanceLinear, Instruments)
		_, err := send(ctx, transport, "/fapi/v1/fundingInfo")
		require.NoError(t, err)
		_, err = send(ctx, transport, "/fapi/v1/fundingInfo")
		assert.ErrorIs(t, err, application.ErrServiceOverloaded)
		assert.Equal(t, 1, c.Attempts(ctx))
		// Family budget still has one charge, and the zero-weight endpoint uses a raw share.
		s := c.scopes[BinanceLinear]
		require.Len(t, s.history, 1)
		assert.Zero(t, s.history[0].cost.weight)
		assert.Equal(t, 1, s.windows["funding_requests_5m"].cost(s.history[0].cost))
		time.Sleep(time.Second)
		_, err = send(ctx, transport, "/fapi/v1/fundingInfo")
		require.NoError(t, err)
		sent := recorder.sent()
		require.Len(t, sent, 2)
		assert.Equal(t, time.Second, sent[1].at.Sub(sent[0].at))
	})
}

func TestWaitAndOperationDeadlines(t *testing.T) {
	for _, tt := range []struct {
		name     string
		timeout  time.Duration
		expected error
	}{{"admission", 20 * time.Second, application.ErrServiceOverloaded}, {"caller", time.Second, context.DeadlineExceeded}} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recorder := &recorder{}
				c, transport := setup(t, config.Defaults(), BinanceSpot, recorder)
				c.scopes[BinanceSpot].next = time.Now().Add(time.Minute)
				ctx, cancel := context.WithTimeout(begin(t, c, BinanceSpot, MarketStats), tt.timeout)
				defer cancel()
				_, err := send(ctx, transport, "/api/v3/ticker/24hr")
				assert.ErrorIs(t, err, tt.expected)
				assert.Empty(t, recorder.sent())
				assert.Zero(t, c.active)
				assert.Zero(t, c.queued)
			})
		})
	}
}

func TestConcurrentRequestsRespectEveryShare(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := smallBybitConfig()
		cfg.Upstream.LanesPerExchange.Tickers.Waiters = 20
		cfg.Upstream.MaxAdmissionWaiters = 100
		recorder := &recorder{}
		c, transport := setup(t, cfg, Bybit, recorder)
		start := time.Now()
		var group sync.WaitGroup
		for i := range 20 {
			ctx := begin(t, c, Bybit, Tickers)
			group.Go(func() {
				_, err := send(ctx, transport, "/v5/market/tickers?category=spot&symbol="+strconv.Itoa(i))
				assert.NoError(t, err)
			})
		}
		group.Wait()
		sent := recorder.sent()
		require.Len(t, sent, 20)
		for i, req := range sent {
			count := 0
			for _, other := range sent[:i+1] {
				if other.at.After(req.at.Add(-5 * time.Second)) {
					count++
				}
			}
			assert.LessOrEqual(t, count, 13)
			if i > 0 {
				assert.GreaterOrEqual(t, req.at.Sub(sent[i-1].at), 10*time.Millisecond)
			}
		}
		assert.Equal(t, start.Add(5*time.Second), sent[13].at)
	})
}
