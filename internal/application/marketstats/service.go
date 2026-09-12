package marketstats

import (
	"context"
	"slices"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"
)

type Query struct {
	application.SnapshotQuery
	Window string
}

type Reader struct {
	repository Repository
	scopes     []application.Scope
}

func NewReader(repository Repository, scopes []application.Scope) *Reader {
	return &Reader{repository: repository, scopes: slices.Clone(scopes)}
}

func (s *Reader) filter(query Query) (Filter, error) {
	selected, err := application.SelectSnapshot(s.scopes, query.SnapshotQuery)
	if err != nil {
		return Filter{}, err
	}

	if query.Window != "" && query.Window != "24h" {
		return Filter{}, application.ErrUnsupportedWindow
	}

	return Filter{SnapshotFilter: selected, Window: 24 * time.Hour}, nil
}

func (s *Reader) Validate(query Query) error {
	_, err := s.filter(query)
	return err
}

func (s *Reader) List(ctx context.Context, query Query) ([]domain.MarketStats, error) {
	filter, err := s.filter(query)
	if err != nil {
		return nil, err
	}

	return s.repository.List(ctx, filter)
}

type Refresher struct {
	provider   Provider
	scope      application.Scope
	repository Repository
	now        func() time.Time
	observe    func(application.RefreshEvent)
}

func NewRefresher(provider Provider, scope application.Scope, repository Repository, now func() time.Time, observe func(application.RefreshEvent)) *Refresher {
	return &Refresher{provider: provider, scope: scope, repository: repository, now: now, observe: observe}
}

func (s *Refresher) Refresh(ctx context.Context) error {
	rows, err := s.provider.GetMarketStats(ctx, s.scope.Market, 24*time.Hour)
	if err == nil {
		err = s.repository.ReplaceSnapshot(ctx, s.scope, 24*time.Hour, rows)
	}

	event := application.RefreshEvent{Scope: s.scope, Window: 24 * time.Hour, Size: len(rows), Error: err}
	if err == nil {
		event.FetchedAt = s.now().UTC()
		if len(rows) > 0 {
			event.FetchedAt = rows[0].FetchedAt
		}
	}

	if s.observe != nil {
		s.observe(event)
	}

	return err
}

func (s *Refresher) Run(ctx context.Context, cycles application.CycleRunner) error {
	return application.RunRefresh(ctx, cycles, s.Refresh)
}
