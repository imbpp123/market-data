package memory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/marketstats"
	"market-data/internal/domain"
)

var _ marketstats.Repository = (*marketstatsRepository)(nil)

type statsScope struct {
	application.Scope
	Window time.Duration
}

type marketstatsRepository struct {
	mu        sync.RWMutex
	snapshots map[statsScope]map[string]domain.MarketStats
}

func NewMarketStatsRepository() marketstats.Repository {
	return &marketstatsRepository{snapshots: make(map[statsScope]map[string]domain.MarketStats)}
}

func (r *marketstatsRepository) ReplaceSnapshot(ctx context.Context, scope application.Scope, window time.Duration, rows []domain.MarketStats) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateScope(scope); err != nil {
		return err
	}
	if window <= 0 {
		return application.ErrUnsupportedWindow
	}
	next := make(map[string]domain.MarketStats, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if row.Exchange != scope.Exchange || row.Market != scope.Market || row.Symbol == "" || row.Window != window {
			return fmt.Errorf("snapshot row does not match scope or has an empty symbol: %w", application.ErrInvalidUpstreamData)
		}
		if _, exists := next[row.Symbol]; exists {
			return fmt.Errorf("duplicate snapshot symbol: %w", application.ErrInvalidUpstreamData)
		}
		next[row.Symbol] = copyMarketStats(row)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	r.snapshots[statsScope{scope, window}] = next
	return nil
}

func (r *marketstatsRepository) HasSnapshot(ctx context.Context, scope application.Scope, window time.Duration) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, ready := r.snapshots[statsScope{scope, window}]
	return ready, nil
}

func (r *marketstatsRepository) List(ctx context.Context, filter marketstats.Filter) ([]domain.MarketStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, scope := range filter.Scopes {
		if _, ready := r.snapshots[statsScope{scope, filter.Window}]; !ready {
			return nil, application.ErrDataNotReady
		}
	}

	result := make([]domain.MarketStats, 0)
	seen := make(map[application.Scope]bool, len(filter.Scopes))
	for _, scope := range filter.Scopes {
		if seen[scope] {
			continue
		}
		seen[scope] = true
		for _, row := range r.snapshots[statsScope{scope, filter.Window}] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if filter.Symbol != nil && row.Symbol != *filter.Symbol {
				continue
			}
			result = append(result, copyMarketStats(row))
		}
	}
	slices.SortFunc(result, func(a, b domain.MarketStats) int {
		return cmp.Or(cmp.Compare(a.Exchange, b.Exchange), cmp.Compare(a.Market, b.Market), cmp.Compare(a.Symbol, b.Symbol))
	})
	return result, nil
}

func (r *marketstatsRepository) Get(ctx context.Context, scope application.Scope, symbol string, window time.Duration) (domain.MarketStats, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return domain.MarketStats{}, false, err
	}
	rows, ready := r.snapshots[statsScope{scope, window}]
	if !ready {
		return domain.MarketStats{}, false, application.ErrDataNotReady
	}
	row, found := rows[symbol]
	return copyMarketStats(row), found, nil
}
