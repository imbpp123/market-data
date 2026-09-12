package exchange_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCandleCapturedCalendarPages(t *testing.T) {
	files, err := filepath.Glob("../../../docs/evidence/phase-01/*-BTCUSDT-*.json")
	require.NoError(t, err)
	eth, err := filepath.Glob("../../../docs/evidence/phase-01/*-ETHUSDT-*.json")
	require.NoError(t, err)
	files = append(files, eth...)
	require.Len(t, files, 12)
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			require.NoError(t, err)
			var fixture struct {
				Exchange domain.Exchange
				Market   domain.Market
				Interval domain.Timeframe
				Raw      json.RawMessage `json:"raw_response"`
				Rows     []struct {
					Exchange                                 domain.Exchange
					Market                                   domain.Market
					Symbol                                   string
					Interval                                 domain.Timeframe
					OpenTime                                 time.Time `json:"open_time"`
					CloseTime                                time.Time `json:"close_time"`
					Open, High, Low, Close, Volume, Turnover decimal.Decimal
					TradesCount                              *int64 `json:"trades_count"`
				} `json:"expected_rows"`
			}
			require.NoError(t, json.Unmarshal(data, &fixture))
			require.NotEmpty(t, fixture.Rows)
			scope := application.Scope{Exchange: fixture.Exchange, Market: fixture.Market}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				assert.Equal(t, fixture.Rows[0].Symbol, r.URL.Query().Get("symbol"))
				_, _ = w.Write(fixture.Raw)
			}))
			defer server.Close()
			provider, admission, id := candleProvider(t, scope, server, config.Defaults(), upstream.SystemClock{}, nil)
			ctx, cancel, err := admission.Begin(t.Context(), id, upstream.Klines)
			require.NoError(t, err)
			defer cancel()
			request := kline.Request{Query: kline.Query{Series: kline.Series{Scope: scope, Symbol: fixture.Rows[0].Symbol, Interval: fixture.Interval}, From: fixture.Rows[0].OpenTime, To: fixture.Rows[len(fixture.Rows)-1].CloseTime}, Limit: len(fixture.Rows)}

			rows, err := provider.GetKlines(ctx, request)

			require.NoError(t, err)
			require.Len(t, rows, len(fixture.Rows))
			for i, want := range fixture.Rows {
				got := rows[i].Candle
				assert.Equal(t, want.Exchange, got.Exchange)
				assert.Equal(t, want.Market, got.Market)
				assert.Equal(t, want.Symbol, got.Symbol)
				assert.Equal(t, want.Interval, got.Interval)
				assert.Equal(t, want.OpenTime, got.OpenTime)
				assert.Equal(t, want.CloseTime, got.CloseTime)
				assert.Equal(t, want.Open.String(), got.Open.String())
				assert.Equal(t, want.High.String(), got.High.String())
				assert.Equal(t, want.Low.String(), got.Low.String())
				assert.Equal(t, want.Close.String(), got.Close.String())
				assert.Equal(t, want.Volume.String(), got.Volume.String())
				assert.Equal(t, want.Turnover.String(), got.Turnover.String())
				assert.Equal(t, want.TradesCount, got.TradesCount)
			}
			assert.Equal(t, 1, admission.Attempts(ctx))
		})
	}
}

type receiptClock struct {
	upstream.SystemClock
	millis atomic.Int64
}

func (c *receiptClock) Now() time.Time { return time.UnixMilli(c.millis.Load()).UTC() }

func TestCandleSuccessfulAttemptEvidenceReachesStorage(t *testing.T) {
	cases := []struct {
		name     string
		retry    bool
		attempts int
		final    bool
	}{
		{"request crosses close", false, 1, false},
		{"retry starts after close", true, 2, true},
	}
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit} {
		for _, tt := range cases {
			t.Run(string(exchange)+"/"+tt.name, func(t *testing.T) {
				scope := application.Scope{Exchange: exchange, Market: domain.MarketSpot}
				request := minuteRequest(scope, 1)
				started := request.From.Add(59 * time.Second)
				clock := &receiptClock{}
				clock.millis.Store(started.UnixMilli())
				var calls atomic.Int64
				var events []upstream.Event
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					clock.millis.Add(2000)
					if calls.Add(1) == 1 && tt.retry {
						w.WriteHeader(500)
						_, _ = io.WriteString(w, `{"code":-1,"retCode":10016}`)
						return
					}
					_, _ = io.WriteString(w, candleBody(scope, request.From, request.To))
				}))
				defer server.Close()
				provider, admission, id := candleProvider(t, scope, server, config.Defaults(), clock, func(e upstream.Event) { events = append(events, e) })
				ctx, cancel, err := admission.Begin(t.Context(), id, upstream.Klines)
				require.NoError(t, err)
				defer cancel()

				rows, err := provider.GetKlines(ctx, request)

				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.Len(t, events, tt.attempts)
				success := events[len(events)-1]
				assert.Equal(t, started.Add(time.Duration(tt.attempts-1)*2*time.Second), rows[0].RequestStartedAt)
				assert.Equal(t, success.StartedAt, rows[0].RequestStartedAt)
				assert.Equal(t, success.StartedAt.Add(success.Duration), rows[0].Candle.FetchedAt)
				assert.Equal(t, tt.attempts, admission.Attempts(ctx))
				repository, err := memory.NewKlineRepository(1000, clock.Now)
				require.NoError(t, err)
				require.NoError(t, repository.UpsertMany(t.Context(), rows))
				stored, err := repository.GetRange(t.Context(), request.Query)
				require.NoError(t, err)
				assert.Equal(t, rows, stored)
				supported, err := provider.SupportedTimeframes(scope.Market)
				require.NoError(t, err)
				planner, err := kline.NewPlanner(scope, supported, 1000, 1000)
				require.NoError(t, err)
				plan, err := planner.Plan(request.Query, stored, clock.Now())
				require.NoError(t, err)
				if tt.final {
					assert.Empty(t, plan)
				} else {
					assert.Equal(t, []kline.Request{request}, plan)
				}
			})
		}
	}
}

func TestCandleFailedLaterPageKeepsOnlyEarlierStoredPage(t *testing.T) {
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit} {
		t.Run(string(exchange), func(t *testing.T) {
			scope := application.Scope{Exchange: exchange, Market: domain.MarketSpot}
			first := minuteRequest(scope, 1)
			clock := &receiptClock{}
			clock.millis.Store(first.To.Add(time.Minute).UnixMilli())
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				clock.millis.Add(1000)
				if calls.Add(1) > 1 {
					w.WriteHeader(500)
					_, _ = io.WriteString(w, `{"code":-1,"retCode":10016}`)
					return
				}
				_, _ = io.WriteString(w, candleBody(scope, first.From, first.To))
			}))
			defer server.Close()
			cfg := config.Defaults()
			cfg.Upstream.LanesPerExchange.Klines.MaxAttempts = 3
			provider, admission, id := candleProvider(t, scope, server, cfg, clock, nil)
			ctx, cancel, err := admission.Begin(t.Context(), id, upstream.Klines)
			require.NoError(t, err)
			defer cancel()
			repository, err := memory.NewKlineRepository(1000, clock.Now)
			require.NoError(t, err)
			saved, err := provider.GetKlines(ctx, first)
			require.NoError(t, err)
			require.NoError(t, repository.UpsertMany(t.Context(), saved))
			second := first
			second.From, second.To = first.To, first.To.Add(time.Minute)

			rows, err := provider.GetKlines(ctx, second)

			assert.ErrorIs(t, err, application.ErrUpstreamAttemptLimit)
			assert.Nil(t, rows)
			assert.Equal(t, 3, admission.Attempts(ctx))
			assert.Equal(t, int64(3), calls.Load())
			query := first.Query
			query.To = second.To
			stored, err := repository.GetRange(t.Context(), query)
			require.NoError(t, err)
			assert.Equal(t, saved, stored)
		})
	}
}
