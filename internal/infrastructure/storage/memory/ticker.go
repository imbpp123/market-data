package memory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	"market-data/internal/application"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"
)

var _ ticker.Repository = (*tickerRepository)(nil)

type tickerRepository struct {
	mu        sync.RWMutex
	snapshots map[application.Scope]map[string]domain.Ticker
}

func NewTickerRepository() ticker.Repository {
	return &tickerRepository{snapshots: make(map[application.Scope]map[string]domain.Ticker)}
}

func (r *tickerRepository) ReplaceSnapshot(ctx context.Context, scope application.Scope, rows []domain.Ticker) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateScope(scope); err != nil {
		return err
	}
	next := make(map[string]domain.Ticker, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if row.Exchange != scope.Exchange || row.Market != scope.Market || row.Symbol == "" {
			return fmt.Errorf("snapshot row does not match scope or has an empty symbol: %w", application.ErrInvalidUpstreamData)
		}
		if _, exists := next[row.Symbol]; exists {
			return fmt.Errorf("duplicate snapshot symbol: %w", application.ErrInvalidUpstreamData)
		}
		next[row.Symbol] = copyTicker(row)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	r.snapshots[scope] = next
	return nil
}

func (r *tickerRepository) HasSnapshot(ctx context.Context, scope application.Scope) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, ready := r.snapshots[scope]
	return ready, nil
}

func (r *tickerRepository) List(ctx context.Context, filter application.SnapshotFilter) ([]domain.Ticker, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, scope := range filter.Scopes {
		if _, ready := r.snapshots[scope]; !ready {
			return nil, application.ErrDataNotReady
		}
	}

	result := make([]domain.Ticker, 0)
	seen := make(map[application.Scope]bool, len(filter.Scopes))
	for _, scope := range filter.Scopes {
		if seen[scope] {
			continue
		}
		seen[scope] = true
		for _, row := range r.snapshots[scope] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if filter.Symbol != nil && row.Symbol != *filter.Symbol {
				continue
			}
			result = append(result, copyTicker(row))
		}
	}
	slices.SortFunc(result, func(a, b domain.Ticker) int {
		return cmp.Or(cmp.Compare(a.Exchange, b.Exchange), cmp.Compare(a.Market, b.Market), cmp.Compare(a.Symbol, b.Symbol))
	})
	return result, nil
}

func (r *tickerRepository) Get(ctx context.Context, scope application.Scope, symbol string) (domain.Ticker, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return domain.Ticker{}, false, err
	}
	rows, ready := r.snapshots[scope]
	if !ready {
		return domain.Ticker{}, false, application.ErrDataNotReady
	}
	row, found := rows[symbol]
	return copyTicker(row), found, nil
}
