package kline_test

import (
	"context"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetentionUsesCalendarSlotsAndKeepsCutoff(t *testing.T) {
	cases := []struct {
		name                         string
		exchange                     domain.Exchange
		interval                     domain.Timeframe
		now, before, cutoff, current string
	}{
		{"minute", domain.ExchangeBinance, domain.Timeframe1m, "2026-09-12T12:00:30Z", "2026-09-12T11:57:00Z", "2026-09-12T11:58:00Z", "2026-09-12T12:00:00Z"},
		{"month", domain.ExchangeBybit, domain.Timeframe1M, "2026-03-15T12:00:00Z", "2025-12-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-03-01T00:00:00Z"},
		{"weekly", domain.ExchangeBybit, domain.Timeframe1w, "2026-09-12T12:00:00Z", "2026-08-17T00:00:00Z", "2026-08-24T00:00:00Z", "2026-09-07T00:00:00Z"},
		{"three day", domain.ExchangeBinance, domain.Timeframe3d, "2026-09-12T12:00:00Z", "2026-09-02T00:00:00Z", "2026-09-05T00:00:00Z", "2026-09-11T00:00:00Z"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			now := parseTime(t, tt.now)
			scope := application.Scope{Exchange: tt.exchange, Market: domain.MarketSpot}
			repository, err := memory.NewKlineRepository(1000, func() time.Time { return now })
			require.NoError(t, err)
			calendar, err := domain.NewCalendar(scope.Exchange, scope.Market, tt.interval)
			require.NoError(t, err)
			rows := []kline.Stored{}
			for _, timestamp := range []string{tt.before, tt.cutoff, tt.current} {
				open := parseTime(t, timestamp)
				closeTime, err := calendar.Next(open)
				require.NoError(t, err)
				rows = append(rows, kline.Stored{Candle: domain.Kline{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT", Interval: tt.interval, OpenTime: open, CloseTime: closeTime, FetchedAt: now}, RequestStartedAt: now})
			}
			require.NoError(t, repository.UpsertMany(t.Context(), rows))
			retention, err := kline.NewRetention(repository, []kline.RetentionScope{{Scope: scope, Interval: tt.interval}}, 2, func() time.Time { return now })
			require.NoError(t, err)

			require.NoError(t, retention.Clean(t.Context()))

			result, err := repository.GetRange(t.Context(), kline.Query{Series: kline.Series{Scope: scope, Symbol: "BTCUSDT", Interval: tt.interval}, From: rows[0].Candle.OpenTime, To: rows[2].Candle.CloseTime})
			require.NoError(t, err)
			require.Len(t, result, 2)
			assert.Equal(t, rows[1].Candle.OpenTime, result[0].Candle.OpenTime)
			assert.Equal(t, rows[2].Candle.OpenTime, result[1].Candle.OpenTime)
		})
	}
}

func parseTime(t *testing.T, text string) time.Time {
	t.Helper()
	result, err := time.Parse(time.RFC3339, text)
	require.NoError(t, err)
	return result
}

type cleanupStore struct {
	kline.Repository
	cutoffs map[kline.RetentionScope]time.Time
	fail    application.Scope
	cancel  context.CancelFunc
}

func (s *cleanupStore) DeleteBefore(ctx context.Context, scope application.Scope, interval domain.Timeframe, before time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.cancel != nil {
		s.cancel()
	}
	if scope == s.fail {
		return application.ErrInternal
	}
	s.cutoffs[kline.RetentionScope{Scope: scope, Interval: interval}] = before
	return nil
}

func TestRetentionUsesOneClockAndContinuesAfterStorageFailure(t *testing.T) {
	spot := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	linear := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}
	store := &cleanupStore{fail: spot, cutoffs: make(map[kline.RetentionScope]time.Time)}
	now := parseTime(t, "2026-09-12T12:00:59Z")
	clockReads := 0
	retention, err := kline.NewRetention(store, []kline.RetentionScope{{Scope: spot, Interval: domain.Timeframe1m}, {Scope: linear, Interval: domain.Timeframe1m}, {Scope: linear, Interval: domain.Timeframe1h}}, 2, func() time.Time {
		clockReads++
		return now.Add(time.Duration(clockReads-1) * time.Hour)
	})
	require.NoError(t, err)

	err = retention.Clean(t.Context())

	assert.ErrorIs(t, err, application.ErrInternal)
	assert.Equal(t, 1, clockReads, "one timestamp is required for the full pass")
	assert.Equal(t, map[kline.RetentionScope]time.Time{
		{Scope: linear, Interval: domain.Timeframe1m}: parseTime(t, "2026-09-12T11:58:00Z"),
		{Scope: linear, Interval: domain.Timeframe1h}: parseTime(t, "2026-09-12T10:00:00Z"),
	}, store.cutoffs)
}

func TestRetentionStopsCanceledPass(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	store := &cleanupStore{cancel: cancel, cutoffs: make(map[kline.RetentionScope]time.Time)}
	retention, err := kline.NewRetention(store, []kline.RetentionScope{{Scope: scope, Interval: domain.Timeframe1m}, {Scope: scope, Interval: domain.Timeframe1h}}, 2, time.Now)
	require.NoError(t, err)

	err = retention.Clean(ctx)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Len(t, store.cutoffs, 1)
}

func TestRetentionRejectsCanceledContextBeforeClock(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	retention, err := kline.NewRetention(&cleanupStore{}, nil, 2, func() time.Time {
		t.Error("clock read after cancellation")
		return time.Time{}
	})
	require.NoError(t, err)

	assert.ErrorIs(t, retention.Clean(ctx), context.Canceled)
}

func TestRetentionReportsCalendarOverflowWithoutDeletion(t *testing.T) {
	store := &cleanupStore{cutoffs: make(map[kline.RetentionScope]time.Time)}
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	retention, err := kline.NewRetention(store, []kline.RetentionScope{{Scope: scope, Interval: domain.Timeframe1M}}, 1000, func() time.Time { return time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC) })
	require.NoError(t, err)

	assert.ErrorIs(t, retention.Clean(t.Context()), domain.ErrTimeOverflow)
	assert.Empty(t, store.cutoffs)
}
