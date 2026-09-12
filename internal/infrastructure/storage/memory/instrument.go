package memory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"
)

var _ instrument.Repository = (*instrumentRepository)(nil)

type instrumentRepository struct {
	mu        sync.RWMutex
	snapshots map[application.Scope]map[string]domain.Instrument
}

func NewInstrumentRepository() instrument.Repository {
	return &instrumentRepository{snapshots: make(map[application.Scope]map[string]domain.Instrument)}
}

func (r *instrumentRepository) ReplaceSnapshot(ctx context.Context, scope application.Scope, rows []domain.Instrument) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateScope(scope); err != nil {
		return err
	}
	next := make(map[string]domain.Instrument, len(rows))
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
		next[row.Symbol] = copyInstrument(row)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	r.snapshots[scope] = next
	return nil
}

func (r *instrumentRepository) HasSnapshot(ctx context.Context, scope application.Scope) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, ready := r.snapshots[scope]
	return ready, nil
}

func (r *instrumentRepository) List(ctx context.Context, filter instrument.Filter) ([]domain.Instrument, error) {
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

	result := make([]domain.Instrument, 0)
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
			if filter.Status != nil && row.Status != *filter.Status {
				continue
			}
			result = append(result, copyInstrument(row))
		}
	}
	slices.SortFunc(result, func(a, b domain.Instrument) int {
		return cmp.Or(cmp.Compare(a.Exchange, b.Exchange), cmp.Compare(a.Market, b.Market), cmp.Compare(a.Symbol, b.Symbol))
	})
	return result, nil
}
