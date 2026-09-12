package memory

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"
)

var _ kline.Repository = (*klineRepository)(nil)

type candleScope struct {
	application.Scope
	Interval domain.Timeframe
}

type klineRepository struct {
	mu             sync.RWMutex
	series         map[kline.Series]map[time.Time]kline.Stored
	cutoffs        map[candleScope]time.Time
	historyCandles int64
	now            func() time.Time
}

// NewKlineRepository uses the API history size and a concurrency-safe clock.
// Cleanup scheduling is owned by the application, not by storage.
func NewKlineRepository(historyCandles int64, now func() time.Time) (kline.Repository, error) {
	if historyCandles <= 0 || now == nil {
		return nil, fmt.Errorf("positive history size and clock are required: %w", application.ErrInvalidParameter)
	}
	return &klineRepository{
		series:         make(map[kline.Series]map[time.Time]kline.Stored),
		cutoffs:        make(map[candleScope]time.Time),
		historyCandles: historyCandles,
		now:            now,
	}, nil
}

func (r *klineRepository) GetRange(ctx context.Context, query kline.Query) ([]kline.Stored, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	calendar, err := seriesCalendar(query.Series)
	if err != nil {
		return nil, err
	}
	if query.From.After(query.To) {
		return nil, application.ErrInvalidRange
	}
	if err := calendar.ValidateBoundary(query.From); err != nil {
		return nil, fmt.Errorf("range start: %w: %w", application.ErrInvalidRange, err)
	}
	if err := calendar.ValidateBoundary(query.To); err != nil {
		return nil, fmt.Errorf("range end: %w: %w", application.ErrInvalidRange, err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([]kline.Stored, 0)
	for open, row := range r.series[query.Series] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !open.Before(query.From) && open.Before(query.To) {
			row.Candle = copyKline(row.Candle)
			result = append(result, row)
		}
	}
	slices.SortFunc(result, func(a, b kline.Stored) int {
		return a.Candle.OpenTime.Compare(b.Candle.OpenTime)
	})
	return result, nil
}

func (r *klineRepository) UpsertMany(ctx context.Context, rows []kline.Stored) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	// Stage the whole batch before touching rows or retention watermarks.
	now := r.now()
	pending := make(map[kline.Series]map[time.Time]kline.Stored)
	cutoffs := make(map[candleScope]time.Time)
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		series := kline.Series{
			Scope:  application.Scope{Exchange: row.Candle.Exchange, Market: row.Candle.Market},
			Symbol: row.Candle.Symbol, Interval: row.Candle.Interval,
		}
		calendar, err := seriesCalendar(series)
		if err != nil {
			return fmt.Errorf("candle series: %w: %w", application.ErrInvalidUpstreamData, err)
		}
		closeTime, err := calendar.Next(row.Candle.OpenTime)
		if err != nil || !closeTime.Equal(row.Candle.CloseTime) || row.Candle.OpenTime.Unix() < 0 {
			return fmt.Errorf("invalid candle boundaries: %w", application.ErrInvalidUpstreamData)
		}
		if row.RequestStartedAt.IsZero() || row.Candle.FetchedAt.IsZero() || row.RequestStartedAt.After(row.Candle.FetchedAt) {
			return fmt.Errorf("invalid candle request timestamps: %w", application.ErrInvalidUpstreamData)
		}
		if row.Candle.OpenTime.After(now) {
			return fmt.Errorf("future candle slot: %w", application.ErrInvalidUpstreamData)
		}
		scope := candleScope{series.Scope, series.Interval}
		if _, exists := cutoffs[scope]; !exists {
			cutoff, err := calendar.HistoryCutoff(now, r.historyCandles)
			if err != nil {
				return fmt.Errorf("history cutoff: %w", err)
			}
			cutoffs[scope] = cutoff
		}
		row.Candle = copyKline(row.Candle)
		row.Candle.OpenTime = row.Candle.OpenTime.UTC()
		row.Candle.CloseTime = row.Candle.CloseTime.UTC()
		if pending[series] == nil {
			pending[series] = make(map[time.Time]kline.Stored)
		}
		if _, exists := pending[series][row.Candle.OpenTime]; exists {
			return fmt.Errorf("duplicate candle key: %w", application.ErrInvalidUpstreamData)
		}
		pending[series][row.Candle.OpenTime] = row
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	for scope, cutoff := range cutoffs {
		if applied, exists := r.cutoffs[scope]; !exists || cutoff.After(applied) {
			r.cutoffs[scope] = cutoff
		}
	}
	for series, incoming := range pending {
		cutoff := r.cutoffs[candleScope{series.Scope, series.Interval}]
		r.prune(series, cutoff)
		for open, row := range incoming {
			if open.Before(cutoff) {
				continue
			}
			stored, exists := r.series[series][open]
			if exists && !replacesCandle(stored, row) {
				continue
			}
			if r.series[series] == nil {
				r.series[series] = make(map[time.Time]kline.Stored)
			}
			r.series[series][open] = row
		}
	}
	return nil
}

func (r *klineRepository) DeleteBefore(ctx context.Context, scope application.Scope, interval domain.Timeframe, before time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateScope(scope); err != nil {
		return err
	}
	calendar, err := domain.NewCalendar(scope.Exchange, scope.Market, interval)
	if err != nil {
		return fmt.Errorf("cleanup calendar: %w: %w", application.ErrInvalidInterval, err)
	}
	if err := calendar.ValidateBoundary(before); err != nil {
		return fmt.Errorf("cleanup cutoff: %w: %w", application.ErrInvalidRange, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	key := candleScope{scope, interval}
	if applied, exists := r.cutoffs[key]; exists && applied.After(before) {
		before = applied
	}
	r.cutoffs[key] = before.UTC()
	for series := range r.series {
		if err := ctx.Err(); err != nil {
			return err
		}
		if series.Scope == scope && series.Interval == interval {
			r.prune(series, before)
		}
	}
	return nil
}

// prune runs under the write lock. Empty series have no retained bookkeeping;
// only the finite exchange/market/interval cutoffs survive their removal.
func (r *klineRepository) prune(series kline.Series, cutoff time.Time) {
	for open := range r.series[series] {
		if open.Before(cutoff) {
			delete(r.series[series], open)
		}
	}
	if len(r.series[series]) == 0 {
		delete(r.series, series)
	}
}

func seriesCalendar(series kline.Series) (domain.Calendar, error) {
	if err := validateScope(series.Scope); err != nil {
		return domain.Calendar{}, err
	}
	if series.Symbol == "" {
		return domain.Calendar{}, application.ErrInvalidFilter
	}
	calendar, err := domain.NewCalendar(series.Exchange, series.Market, series.Interval)
	if err != nil {
		return domain.Calendar{}, fmt.Errorf("series calendar: %w: %w", application.ErrInvalidInterval, err)
	}
	return calendar, nil
}

func replacesCandle(stored, incoming kline.Stored) bool {
	if !stored.RequestStartedAt.Before(stored.Candle.CloseTime) {
		return false
	}
	if !incoming.RequestStartedAt.Before(incoming.Candle.CloseTime) {
		return true
	}
	return incoming.RequestStartedAt.After(stored.RequestStartedAt) ||
		(incoming.RequestStartedAt.Equal(stored.RequestStartedAt) && incoming.Candle.FetchedAt.After(stored.Candle.FetchedAt))
}

func (r *klineRepository) CandleCounts(ctx context.Context) (map[application.Scope]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	counts := make(map[application.Scope]int)
	for series, rows := range r.series {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		counts[series.Scope] += len(rows)
	}
	return counts, nil
}
