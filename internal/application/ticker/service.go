package ticker

import (
	"context"
	"errors"
	"slices"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/marketstats"
)

type Reader struct {
	repository Repository
	scopes     []application.Scope
	builder    *ReadModelBuilder
}

func NewReader(repository Repository, scopes []application.Scope, now func() time.Time) *Reader {
	return &Reader{repository: repository, scopes: slices.Clone(scopes), builder: NewReadModelBuilder(now)}
}

func (s *Reader) Validate(query application.SnapshotQuery) error {
	_, err := application.SelectSnapshot(s.scopes, query)
	return err
}

func (s *Reader) List(ctx context.Context, query application.SnapshotQuery) ([]ReadModel, error) {
	filter, err := application.SelectSnapshot(s.scopes, query)
	if err != nil {
		return nil, err
	}

	rows, err := s.repository.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	return s.builder.Build(rows), nil
}

type Refresher struct {
	provider   Provider
	scope      application.Scope
	repository Repository
	stats      marketstats.Repository
	observe    func(application.RefreshEvent)
}

func NewRefresher(provider Provider, scope application.Scope, repository Repository, stats marketstats.Repository, observe func(application.RefreshEvent)) *Refresher {
	return &Refresher{provider: provider, scope: scope, repository: repository, stats: stats, observe: observe}
}

func (s *Refresher) Refresh(ctx context.Context) error {
	result, err := s.provider.GetTickers(ctx, s.scope.Market)
	shared := s.provider.Capabilities().MarketStatsWithTicker
	if err != nil {
		result.TickerError = err
		result.MarketStatsError = err
	}

	if shared && err == nil && !result.HasMarketStats {
		result.MarketStatsError = application.ErrInvalidUpstreamData
	}

	tickerError := result.TickerError
	if tickerError == nil {
		tickerError = s.repository.ReplaceSnapshot(ctx, s.scope, result.Tickers)
	}

	event := application.RefreshEvent{Scope: s.scope, Error: tickerError, Size: len(result.Tickers), FetchedAt: result.FetchedAt}
	if len(result.Tickers) > 0 {
		event.FetchedAt = result.Tickers[0].FetchedAt
	}

	if s.observe != nil && application.DeferredRefresh(event.Error) == nil {
		s.observe(event)
	}

	if !shared {
		return tickerError
	}

	statsError := result.MarketStatsError
	if statsError == nil {
		statsError = s.stats.ReplaceSnapshot(ctx, s.scope, 24*time.Hour, result.MarketStats)
	}

	event = application.RefreshEvent{Scope: s.scope, Window: 24 * time.Hour, Error: statsError, Size: len(result.MarketStats), FetchedAt: result.FetchedAt}
	if len(result.MarketStats) > 0 {
		event.FetchedAt = result.MarketStats[0].FetchedAt
	}

	if s.observe != nil && application.DeferredRefresh(event.Error) == nil {
		s.observe(event)
	}

	return errors.Join(tickerError, statsError)
}

func (s *Refresher) Run(ctx context.Context, cycles application.CycleRunner) error {
	return application.RunRefresh(ctx, cycles, s.Refresh)
}
