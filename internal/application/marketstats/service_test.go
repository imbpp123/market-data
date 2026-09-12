package marketstats_test

import (
	"context"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/marketstats"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type statsProvider struct {
	rows []domain.MarketStats
	err  error
}

func (statsProvider) Exchange() domain.Exchange { return domain.ExchangeBinance }

func (statsProvider) Capabilities() application.ExchangeCapabilities {
	return application.ExchangeCapabilities{MarketStatsWindows: []time.Duration{24 * time.Hour}}
}

func (p statsProvider) GetMarketStats(ctx context.Context, _ domain.Market, _ time.Duration) ([]domain.MarketStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return p.rows, p.err
}

type failedStore struct{ marketstats.Repository }

func (failedStore) ReplaceSnapshot(context.Context, application.Scope, time.Duration, []domain.MarketStats) error {
	return application.ErrInternal
}

func TestRefreshPreservesReceiptTimeAndPriorSnapshot(t *testing.T) {
	cases := []struct {
		name                   string
		failure                error
		write, empty, canceled bool
	}{
		{name: "success"}, {name: "empty", empty: true}, {name: "fetch error", failure: application.ErrUpstream}, {name: "normalization", failure: application.ErrInvalidUpstreamData}, {name: "write error", write: true}, {name: "canceled", canceled: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
			old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			received := old.Add(time.Second)
			repo := memory.NewMarketStatsRepository()
			require.NoError(t, repo.ReplaceSnapshot(t.Context(), scope, 24*time.Hour, []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "OLD", Window: 24 * time.Hour, FetchedAt: old}}))
			source := statsProvider{err: tt.failure}
			if !tt.empty {
				source.rows = []domain.MarketStats{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "NEW", Window: 24 * time.Hour, FetchedAt: received}}
			}

			target := repo
			if tt.write {
				target = failedStore{repo}
			}

			var event application.RefreshEvent
			refresher := marketstats.NewRefresher(source, scope, target, func() time.Time { return received.Add(time.Hour) }, func(e application.RefreshEvent) { event = e })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}

			err := refresher.Refresh(ctx)

			failed := tt.failure != nil || tt.write || tt.canceled
			if failed {
				require.Error(t, err)
				assert.Error(t, event.Error)
			} else {
				require.NoError(t, err)
				assert.NoError(t, event.Error)
			}

			rows, err := repo.List(t.Context(), marketstats.Filter{SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{scope}}, Window: 24 * time.Hour})
			require.NoError(t, err)
			if tt.empty {
				assert.Empty(t, rows)
				return
			}

			require.Len(t, rows, 1)
			if failed {
				assert.Equal(t, "OLD", rows[0].Symbol)
				assert.Equal(t, old, rows[0].FetchedAt)
			} else {
				assert.Equal(t, "NEW", rows[0].Symbol)
				assert.Equal(t, received, rows[0].FetchedAt)
				assert.Equal(t, received, event.FetchedAt)
			}
		})
	}
}

func TestUnsupportedWindowFailsBeforeRepositoryAccess(t *testing.T) {
	scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	reader := marketstats.NewReader(nil, []application.Scope{scope})

	rows, err := reader.List(t.Context(), marketstats.Query{Window: "2h"})

	assert.ErrorIs(t, err, application.ErrUnsupportedWindow)
	assert.Nil(t, rows)
}
