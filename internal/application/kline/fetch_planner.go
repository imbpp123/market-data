package kline

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"
)

// Planner uses one enabled scope's provider support list and final configuration.
// It owns no repository, clock, or upstream client.
type Planner struct {
	scope          application.Scope
	supported      []domain.Timeframe
	historyCandles int64
	pageLimit      int
}

func NewPlanner(scope application.Scope, supported []domain.Timeframe, historyCandles int64, pageLimit int) (*Planner, error) {
	if !scope.Exchange.Valid() || !scope.Market.Valid() || len(supported) == 0 || historyCandles <= 0 || pageLimit <= 0 {
		return nil, application.ErrInvalidParameter
	}

	for _, interval := range supported {
		if _, err := domain.NewCalendar(scope.Exchange, scope.Market, interval); err != nil {
			return nil, fmt.Errorf("supported calendar: %w: %w", application.ErrInvalidParameter, err)
		}
	}

	return &Planner{
		scope: scope, supported: slices.Clone(supported),
		historyCandles: historyCandles, pageLimit: pageLimit,
	}, nil
}

// Validate must run before cache lookup. Symbol existence and instrument
// readiness are checked by the caller after this local validation.
func (p *Planner) Validate(query Query, now time.Time) error {
	_, _, err := p.validate(query, now)
	return err
}

func (p *Planner) validate(query Query, now time.Time) (domain.Calendar, int64, error) {
	var calendar domain.Calendar
	if p.historyCandles <= 0 || p.pageLimit <= 0 {
		return calendar, 0, application.ErrInvalidParameter
	}

	if query.Scope != p.scope || query.Symbol == "" {
		return calendar, 0, application.ErrInvalidFilter
	}

	_, err := application.SelectSnapshot([]application.Scope{p.scope}, application.SnapshotQuery{
		Exchange: string(query.Exchange), Market: string(query.Market), Symbol: query.Symbol,
	})
	if err != nil {
		return calendar, 0, err
	}

	if !query.Interval.Valid() || !slices.Contains(p.supported, query.Interval) {
		return calendar, 0, application.ErrInvalidInterval
	}

	calendar, err = domain.NewCalendar(query.Exchange, query.Market, query.Interval)
	if err != nil {
		return calendar, 0, fmt.Errorf("query calendar: %w: %w", application.ErrInvalidInterval, err)
	}

	if query.From.Unix() < 0 || query.To.Unix() < 0 || query.From.After(query.To) {
		return calendar, 0, application.ErrInvalidRange
	}

	// Check shape before size, then depth. Counting does not allocate slots.
	count, countErr := calendar.CountSlots(query.From, query.To, p.historyCandles)
	if countErr != nil && !errors.Is(countErr, domain.ErrSlotLimit) {
		return calendar, 0, fmt.Errorf("query boundaries: %w: %w", application.ErrInvalidRange, countErr)
	}

	current, err := calendar.Floor(now)
	if err != nil {
		return calendar, 0, fmt.Errorf("planning clock: %w: %w", application.ErrInvalidRange, err)
	}

	if query.To.After(current) {
		next, err := calendar.Next(current)
		if err != nil {
			return calendar, 0, fmt.Errorf("current slot end: %w: %w", application.ErrInvalidRange, err)
		}
		if query.To.After(next) || (query.From.After(current) && !query.From.Equal(query.To)) {
			return calendar, 0, application.ErrInvalidRange
		}
	}

	if countErr != nil {
		return calendar, 0, application.ErrRequestTooLarge
	}

	cutoff, err := calendar.HistoryCutoff(now, p.historyCandles)
	if err != nil {
		return calendar, 0, fmt.Errorf("history cutoff: %w: %w", application.ErrInvalidRange, err)
	}
	if query.From.Before(cutoff) {
		return calendar, 0, application.ErrRangeOutOfRetention
	}

	return calendar, count, nil
}

// Plan revalidates the rolling window on each pass. Cached rows must follow
// Repository.GetRange's contract, except that their order does not matter.
// Every non-final row requires a refresh, even after its local close boundary.
func (p *Planner) Plan(query Query, cached []Stored, now time.Time) ([]Request, error) {
	calendar, count, err := p.validate(query, now)
	if err != nil {
		return nil, err
	}

	final, err := finalSlots(query, calendar, count, cached, now)
	if err != nil {
		return nil, err
	}

	requests := make([]Request, 0)
	var pending Request
	var start int64
	open := query.From.UTC()
	for slot := int64(0); slot < count; slot++ {
		next, err := calendar.Next(open)
		if err != nil || !next.After(open) || next.After(query.To) {
			return nil, fmt.Errorf("slot does not advance within query: %w", application.ErrInvalidRange)
		}

		if !final[open] {
			if pending.Limit > 0 && slot-start >= int64(p.pageLimit) {
				requests = append(requests, pending)
				pending = Request{}
			}
			if pending.Limit == 0 {
				pending.Query = Query{Series: query.Series, From: open}
				start = slot
			}
			// Cached slots between required slots share the page. Trailing
			// final slots cannot reduce request count, so leave them out.
			pending.To = next
			pending.Limit = int(slot-start) + 1
		}

		open = next
	}
	if pending.Limit > 0 {
		requests = append(requests, pending)
	}

	return requests, nil
}

func finalSlots(query Query, calendar domain.Calendar, count int64, cached []Stored, now time.Time) (map[time.Time]bool, error) {
	if int64(len(cached)) > count {
		return nil, fmt.Errorf("too many cached rows: %w", application.ErrInternal)
	}

	final := make(map[time.Time]bool, len(cached))
	for _, row := range cached {
		candle := row.Candle
		if candle.Exchange != query.Exchange || candle.Market != query.Market || candle.Symbol != query.Symbol || candle.Interval != query.Interval {
			return nil, fmt.Errorf("cached candle series mismatch: %w", application.ErrInternal)
		}
		if candle.OpenTime.Before(query.From) || !candle.OpenTime.Before(query.To) {
			return nil, fmt.Errorf("cached candle outside query: %w", application.ErrInternal)
		}
		closeTime, err := calendar.Next(candle.OpenTime)
		if err != nil || !closeTime.Equal(candle.CloseTime) {
			return nil, fmt.Errorf("invalid cached candle boundaries: %w", application.ErrInternal)
		}
		if row.RequestStartedAt.IsZero() || candle.FetchedAt.IsZero() || row.RequestStartedAt.After(candle.FetchedAt) {
			return nil, fmt.Errorf("invalid cached request timestamps: %w", application.ErrInternal)
		}

		open := candle.OpenTime.UTC()
		if _, exists := final[open]; exists {
			return nil, fmt.Errorf("duplicate cached candle: %w", application.ErrInternal)
		}
		final[open] = !now.Before(closeTime) && !row.RequestStartedAt.Before(closeTime)
	}

	return final, nil
}
