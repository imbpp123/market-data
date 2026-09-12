package ticker_test

import (
	"context"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type currentProvider struct {
	result ticker.Collection
	err    error
	shared bool
}

func (p currentProvider) Exchange() domain.Exchange { return domain.ExchangeBybit }

func (p currentProvider) Capabilities() application.ExchangeCapabilities {
	return application.ExchangeCapabilities{MarketStatsWithTicker: p.shared, MarketStatsWindows: []time.Duration{24 * time.Hour}}
}

func (p currentProvider) GetTickers(ctx context.Context, _ domain.Market) (ticker.Collection, error) {
	if err := ctx.Err(); err != nil {
		return ticker.Collection{}, err
	}

	return p.result, p.err
}

type failingTickerStore struct{ ticker.Repository }

func (failingTickerStore) ReplaceSnapshot(context.Context, application.Scope, []domain.Ticker) error {
	return application.ErrInternal
}

type failingStatsStore struct{ marketstats.Repository }

func (failingStatsStore) ReplaceSnapshot(context.Context, application.Scope, time.Duration, []domain.MarketStats) error {
	return application.ErrInternal
}

func TestIndependentPublication(t *testing.T) {
	cases := []struct {
		name                                              string
		tickerError, statsError, outer                    error
		tickerWrite, statsWrite, missing, canceled, empty bool
	}{
		{name: "both succeed"},
		{name: "empty success", empty: true},
		{name: "ticker normalization", tickerError: application.ErrInvalidUpstreamData},
		{name: "stats normalization", statsError: application.ErrInvalidUpstreamData},
		{name: "ticker write", tickerWrite: true},
		{name: "stats write", statsWrite: true},
		{name: "transport error", outer: application.ErrUpstream},
		{name: "envelope error", outer: application.ErrInvalidUpstreamData},
		{name: "missing shared branch", missing: true},
		{name: "canceled", canceled: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			scope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}
			old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			next := old.Add(time.Minute)
			tickers, stats := memory.NewTickerRepository(), memory.NewMarketStatsRepository()
			require.NoError(t, tickers.ReplaceSnapshot(t.Context(), scope, []domain.Ticker{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "OLD", FetchedAt: old}}))
			require.NoError(t, stats.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "OLD", Window: 24 * time.Hour, FetchedAt: old}}))
			tickerTarget, statsTarget := tickers, stats
			if tt.tickerWrite {
				tickerTarget = failingTickerStore{tickers}
			}

			if tt.statsWrite {
				statsTarget = failingStatsStore{stats}
			}

			result := ticker.Collection{FetchedAt: next, HasMarketStats: !tt.missing, TickerError: tt.tickerError, MarketStatsError: tt.statsError}
			if !tt.empty {
				result.Tickers = []domain.Ticker{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "NEW", FetchedAt: next}}
				result.MarketStats = []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "NEW", Window: 24 * time.Hour, FetchedAt: next}}
			}

			var events []application.RefreshEvent
			refresher := ticker.NewRefresher(currentProvider{result: result, err: tt.outer, shared: true}, scope, tickerTarget, statsTarget, func(e application.RefreshEvent) { events = append(events, e) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}

			err := refresher.Refresh(ctx)

			tickerFailed := tt.tickerError != nil || tt.tickerWrite || tt.outer != nil || tt.canceled
			statsFailed := tt.statsError != nil || tt.statsWrite || tt.outer != nil || tt.canceled || tt.missing
			if tickerFailed || statsFailed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			tr, err := tickers.List(t.Context(), application.SnapshotFilter{Scopes: []application.Scope{scope}})
			require.NoError(t, err)
			sr, err := stats.List(t.Context(), marketstats.Filter{SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{scope}}, Window: 24 * time.Hour})
			require.NoError(t, err)
			if tt.empty {
				assert.Empty(t, tr)
				assert.Empty(t, sr)
			} else {
				require.Len(t, tr, 1)
				require.Len(t, sr, 1)
				if tickerFailed {
					assert.Equal(t, "OLD", tr[0].Symbol)
					assert.Equal(t, old, tr[0].FetchedAt)
				} else {
					assert.Equal(t, "NEW", tr[0].Symbol)
					assert.Equal(t, next, tr[0].FetchedAt)
				}

				if statsFailed {
					assert.Equal(t, "OLD", sr[0].Symbol)
					assert.Equal(t, old, sr[0].FetchedAt)
				} else {
					assert.Equal(t, "NEW", sr[0].Symbol)
					assert.Equal(t, next, sr[0].FetchedAt)
				}
			}

			require.Len(t, events, 2)
			assert.Equal(t, tickerFailed, events[0].Error != nil)
			assert.Equal(t, statsFailed, events[1].Error != nil)
			assert.Zero(t, events[0].Window)
			assert.Equal(t, 24*time.Hour, events[1].Window)
		})
	}
}

func TestIndependentTickerDoesNotPublishStatistics(t *testing.T) {
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	tickers := memory.NewTickerRepository()
	stats := memory.NewMarketStatsRepository()
	refresher := ticker.NewRefresher(currentProvider{shared: false}, scope, tickers, stats, nil)

	require.NoError(t, refresher.Refresh(t.Context()))

	ready, err := tickers.HasSnapshot(t.Context(), scope)
	require.NoError(t, err)
	assert.True(t, ready)
	ready, err = stats.HasSnapshot(t.Context(), scope, 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, ready)
}
