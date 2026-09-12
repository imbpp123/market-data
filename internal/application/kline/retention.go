package kline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"
)

// Inventory reports retained rows without copying candle payloads.
type Inventory interface {
	CandleCounts(context.Context) (map[application.Scope]int, error)
}

type RetentionScope struct {
	application.Scope
	Interval domain.Timeframe
}

type Retention struct {
	repository Repository
	scopes     []RetentionScope
	history    int64
	now        func() time.Time
}

func NewRetention(repository Repository, scopes []RetentionScope, history int64, now func() time.Time) (*Retention, error) {
	if repository == nil || history <= 0 || now == nil {
		return nil, application.ErrInvalidParameter
	}
	for _, scope := range scopes {
		if _, err := domain.NewCalendar(scope.Exchange, scope.Market, scope.Interval); err != nil {
			return nil, err
		}
	}
	return &Retention{repository: repository, scopes: slices.Clone(scopes), history: history, now: now}, nil
}

// Clean continues independent scopes after a storage failure. Cancellation stops
// the pass; successful scopes keep their monotonic storage cutoffs.
func (r *Retention) Clean(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := r.now()
	var failures error
	for _, scope := range r.scopes {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		calendar, err := domain.NewCalendar(scope.Exchange, scope.Market, scope.Interval)
		if err != nil {
			return err
		}
		cutoff, err := calendar.HistoryCutoff(now, r.history)
		if err == nil {
			err = r.repository.DeleteBefore(ctx, scope.Scope, scope.Interval, cutoff)
		}
		if err != nil {
			failures = errors.Join(failures, fmt.Errorf("retention %s/%s/%s: %w", scope.Exchange, scope.Market, scope.Interval, err))
		}
	}
	return failures
}
