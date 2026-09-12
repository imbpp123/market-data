package kline

import (
	"context"
	"maps"
	"strings"
	"time"

	"market-data/internal/application"
)

type callerBudget struct {
	remaining int // Guarded by Service.mu while attached to a fill.
	exhausted chan struct{}
}

type fill struct {
	done      chan struct{}
	callers   map[*callerBudget]struct{}
	refreshed refreshes
	saved     refreshes
	err       error
	attempts  int
}

func (s *Service) join(ctx context.Context, query Query, refreshed refreshes, budget *callerBudget) (*fill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil, context.Canceled
	}
	if err := s.contextError(ctx); err != nil {
		return nil, err
	}
	if f := s.fills[query.Series]; f != nil {
		// Conservatively include earlier dispatches from this fill, including
		// its current attempt. Later fills cannot reset the caller's budget.
		if f.attempts > budget.remaining {
			return nil, application.ErrUpstreamAttemptLimit
		}
		budget.remaining -= f.attempts
		f.callers[budget] = struct{}{}
		s.emit(Event{Scope: query.Scope, SharedWait: true})
		return f, nil
	}
	if len(s.fills) >= s.settings.MaxActiveFills || s.active[query.Exchange] >= s.settings.MaxActiveFillsPerExchange {
		return nil, application.ErrServiceOverloaded
	}

	f := &fill{done: make(chan struct{}), callers: map[*callerBudget]struct{}{budget: {}}, refreshed: maps.Clone(refreshed), saved: make(refreshes)}
	s.fills[query.Series] = f
	s.active[query.Exchange]++
	s.owned.Add(1)
	s.emit(Event{Scope: query.Scope, FillStarted: true})
	key := strings.Join([]string{string(query.Exchange), string(query.Market), query.Symbol, string(query.Interval)}, "\x00")
	// Share one completion broadcast per fill. Registering a DoChan result
	// for every waiter would retain canceled callers until the fill ends.
	result := s.group.DoChan(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(s.root, s.settings.FillTimeout)
		defer cancel()
		err := s.operations.Run(ctx, query.Scope, func() { s.attempt(query, f) }, func(ctx context.Context) error {
			return s.load(ctx, query, f)
		})
		return nil, err
	})
	go func() {
		defer s.owned.Done()
		completed := <-result
		s.mu.Lock()
		defer s.mu.Unlock()
		f.err = completed.Err
		delete(s.fills, query.Series)
		s.active[query.Exchange]--
		close(f.done)
	}()
	return f, nil
}

func (s *Service) attempt(query Query, f *fill) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f.attempts++
	for budget := range f.callers {
		if budget.remaining == 0 {
			close(budget.exhausted)
			delete(f.callers, budget)
		} else {
			budget.remaining--
		}
	}
	s.emit(Event{Scope: query.Scope, Attempts: 1})
}

func (s *Service) wait(ctx context.Context, f *fill, budget *callerBudget) error {
	defer func() {
		s.mu.Lock()
		delete(f.callers, budget)
		s.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.root.Done():
		return s.root.Err()
	case <-budget.exhausted:
		return application.ErrUpstreamAttemptLimit
	case <-f.done:
		select {
		case <-budget.exhausted:
			return application.ErrUpstreamAttemptLimit
		default:
			return f.err
		}
	}
}

func (s *Service) load(ctx context.Context, query Query, f *fill) error {
	scope := s.scopes[query.Scope]
	rows, plan, err := s.read(ctx, scope.planner, query, f.refreshed)
	for err == nil && len(plan) > 0 {
		s.mu.Lock()
		remaining := s.settings.MaxAttempts - f.attempts
		s.mu.Unlock()
		if len(plan) > remaining {
			return application.ErrUpstreamAttemptLimit
		}

		for _, request := range plan {
			if err := scope.planner.Validate(query, s.now()); err != nil {
				return err
			}
			page, err := scope.provider.GetKlines(ctx, request)
			if err != nil {
				return err
			}
			s.emit(Event{Scope: query.Scope, Downloaded: len(page)})
			if err := s.repository.UpsertMany(ctx, page); err != nil {
				return err
			}
			for _, row := range page {
				updated := refreshes{row.Candle.OpenTime.UTC(): {started: row.RequestStartedAt, fetched: row.Candle.FetchedAt}}
				f.refreshed.merge(query, updated)
				f.saved.merge(query, updated)
			}
		}

		previous := rows
		rows, plan, err = s.read(ctx, scope.planner, query, f.refreshed)
		if err == nil && len(plan) > 0 && !madeProgress(previous, rows) {
			return application.ErrIncompleteData
		}
	}
	return err
}

func madeProgress(before, after []Stored) bool {
	previous := make(map[time.Time]Stored, len(before))
	for _, row := range before {
		previous[row.Candle.OpenTime.UTC()] = row
	}
	for _, row := range after {
		old, ok := previous[row.Candle.OpenTime.UTC()]
		if !ok || row.RequestStartedAt.After(old.RequestStartedAt) ||
			(row.RequestStartedAt.Equal(old.RequestStartedAt) && row.Candle.FetchedAt.After(old.Candle.FetchedAt)) {
			return true
		}
	}
	return false
}
