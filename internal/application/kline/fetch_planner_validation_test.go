package kline

import (
	"math"
	"strings"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlannerRangeValidation(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		to      string
		wantErr error
	}{
		{"full history", "10:00:00Z", "10:40:00Z", nil},
		{"current candle", "10:40:00Z", "10:45:00Z", nil},
		{"history and open within bound", "10:05:00Z", "10:45:00Z", nil},
		{"open consumes history request slot", "10:00:00Z", "10:45:00Z", application.ErrRequestTooLarge},
		{"one old slot", "09:55:00Z", "10:00:00Z", application.ErrRangeOutOfRetention},
		{"size before history", "09:55:00Z", "10:40:00Z", application.ErrRequestTooLarge},
		{"empty current", "10:40:00Z", "10:40:00Z", nil},
		{"empty next boundary", "10:45:00Z", "10:45:00Z", nil},
		{"empty at cutoff", "10:00:00Z", "10:00:00Z", nil},
		{"empty old", "09:55:00Z", "09:55:00Z", application.ErrRangeOutOfRetention},
		{"reversed", "10:05:00Z", "10:00:00Z", application.ErrInvalidRange},
		{"future end", "10:40:00Z", "10:50:00Z", application.ErrInvalidRange},
		{"future start", "10:45:00Z", "10:50:00Z", application.ErrInvalidRange},
		{"empty future", "10:50:00Z", "10:50:00Z", application.ErrInvalidRange},
		{"shape before size", "09:00:00Z", "11:00:00Z", application.ErrInvalidRange},
		{"unaligned start", "10:00:01Z", "10:40:00Z", application.ErrInvalidRange},
		{"unaligned end", "10:00:00Z", "10:39:00Z", application.ErrInvalidRange},
		{"fractional boundary", "10:00:00.000000001Z", "10:40:00Z", application.ErrInvalidRange},
		{"empty unaligned", "10:00:01Z", "10:00:01Z", application.ErrInvalidRange},
		{"timezone offset", "12:00:00+02:00", "12:40:00+02:00", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := plannerQuery(t, domain.Timeframe5m, "2026-09-11T"+tc.from, "2026-09-11T"+tc.to)
			p := testPlanner(t, query, 8, 8)
			now := plannerTime(t, "2026-09-11T10:40:30Z")

			err := p.Validate(query, now)
			plan, planErr := p.Plan(query, nil, now)

			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				assert.ErrorIs(t, planErr, tc.wantErr)
				assert.Nil(t, plan)
				return
			}
			require.NoError(t, err)
			require.NoError(t, planErr)
			if query.From.Equal(query.To) {
				assert.Empty(t, plan)
			} else {
				require.Len(t, plan, 1)
				assert.Equal(t, query.From.UTC(), plan[0].From)
				assert.Equal(t, query.To.UTC(), plan[0].To)
			}
		})
	}
}

func TestPlannerRejectsInvalidSeriesBeforeRangeOrCache(t *testing.T) {
	cases := []struct {
		name    string
		change  func(*Query)
		wantErr error
	}{
		{"unknown exchange", func(q *Query) { q.Exchange = "other" }, application.ErrInvalidFilter},
		{"different exchange", func(q *Query) { q.Exchange = domain.ExchangeBybit }, application.ErrInvalidFilter},
		{"unknown market", func(q *Query) { q.Market = "inverse" }, application.ErrInvalidFilter},
		{"different market", func(q *Query) { q.Market = domain.MarketLinear }, application.ErrInvalidFilter},
		{"empty symbol", func(q *Query) { q.Symbol = "" }, application.ErrInvalidFilter},
		{"long symbol", func(q *Query) { q.Symbol = strings.Repeat("A", 129) }, application.ErrInvalidFilter},
		{"symbol whitespace", func(q *Query) { q.Symbol = "BTC USDT" }, application.ErrInvalidFilter},
		{"symbol control", func(q *Query) { q.Symbol = "BTC\x00" }, application.ErrInvalidFilter},
		{"symbol encoding", func(q *Query) { q.Symbol = "\xff" }, application.ErrInvalidFilter},
		{"zero interval", func(q *Query) { q.Interval = "" }, application.ErrInvalidInterval},
		{"unknown interval", func(q *Query) { q.Interval = "60m" }, application.ErrInvalidInterval},
		{"interval whitespace", func(q *Query) { q.Interval = " 5m" }, application.ErrInvalidInterval},
		{"unsupported interval", func(q *Query) { q.Interval = domain.Timeframe1s }, application.ErrInvalidInterval},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := plannerQuery(t, domain.Timeframe5m, "2026-09-11T10:00:00Z", "2026-09-11T10:40:00Z")
			p := testPlanner(t, query, 8, 8)
			tc.change(&query)
			query.From = query.To.Add(time.Hour)

			err := p.Validate(query, query.To)
			plan, planErr := p.Plan(query, []Stored{{}}, query.To)

			assert.ErrorIs(t, err, tc.wantErr)
			assert.ErrorIs(t, planErr, tc.wantErr)
			assert.Nil(t, plan)
		})
	}
}

func TestPlannerCalendarBounds(t *testing.T) {
	cases := []struct {
		name          string
		interval      domain.Timeframe
		from, to, now string
		history       int64
		wantErr       error
	}{
		{"pre epoch", domain.Timeframe1m, "1969-12-31T23:59:00Z", "1970-01-01T00:00:00Z", "1970-01-01T00:00:00Z", 8, application.ErrInvalidRange},
		{"cutoff before epoch", domain.Timeframe1m, "1970-01-01T00:00:00Z", "1970-01-01T00:01:00Z", "1970-01-01T00:01:00Z", 8, nil},
		{"centuries of seconds", domain.Timeframe1s, "1970-01-01T00:00:00Z", "9999-12-31T23:59:59Z", "9999-12-31T23:59:59Z", 1000, application.ErrRequestTooLarge},
		{"history overflow", domain.Timeframe1m, "2026-09-11T10:00:00Z", "2026-09-11T10:01:00Z", "2026-09-11T10:01:00Z", math.MaxInt64, application.ErrInvalidRange},
		{"monthly history overflow", domain.Timeframe1M, "2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z", "2026-09-01T00:00:00Z", math.MaxInt64, application.ErrInvalidRange},
		{"monthly depth", domain.Timeframe1M, "2028-01-01T00:00:00Z", "2028-02-01T00:00:00Z", "2028-04-30T23:59:59Z", 2, application.ErrRangeOutOfRetention},
		{"monthly cutoff", domain.Timeframe1M, "2028-02-01T00:00:00Z", "2028-04-01T00:00:00Z", "2028-04-30T23:59:59Z", 2, nil},
		{"wrong weekly anchor", domain.Timeframe1w, "2026-01-04T00:00:00Z", "2026-01-11T00:00:00Z", "2026-01-12T00:00:00Z", 8, application.ErrInvalidRange},
		{"wrong three day anchor", domain.Timeframe3d, "2026-01-01T00:00:00Z", "2026-01-04T00:00:00Z", "2026-01-05T00:00:00Z", 8, application.ErrInvalidRange},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := plannerQuery(t, tc.interval, tc.from, tc.to)
			p := testPlanner(t, query, tc.history, math.MaxInt)

			plan, err := p.Plan(query, nil, plannerTime(t, tc.now))

			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				assert.Nil(t, plan)
			} else {
				require.NoError(t, err)
				require.Len(t, plan, 1)
				assert.Equal(t, query, plan[0].Query)
			}
		})
	}
}

func TestPlannerRejectsInvalidCachedRows(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Stored)
	}{
		{"exchange", func(r *Stored) { r.Candle.Exchange = domain.ExchangeBybit }},
		{"market", func(r *Stored) { r.Candle.Market = domain.MarketLinear }},
		{"symbol", func(r *Stored) { r.Candle.Symbol = "ETHUSDT" }},
		{"interval", func(r *Stored) { r.Candle.Interval = domain.Timeframe1m }},
		{"before query", func(r *Stored) { r.Candle.OpenTime = r.Candle.OpenTime.Add(-5 * time.Minute) }},
		{"at query end", func(r *Stored) { r.Candle.OpenTime = r.Candle.OpenTime.Add(10 * time.Minute) }},
		{"unaligned open", func(r *Stored) { r.Candle.OpenTime = r.Candle.OpenTime.Add(time.Second) }},
		{"wrong close", func(r *Stored) { r.Candle.CloseTime = r.Candle.CloseTime.Add(-time.Millisecond) }},
		{"missing request start", func(r *Stored) { r.RequestStartedAt = time.Time{} }},
		{"missing receipt", func(r *Stored) { r.Candle.FetchedAt = time.Time{} }},
		{"receipt before start", func(r *Stored) { r.Candle.FetchedAt = r.RequestStartedAt.Add(-time.Second) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := plannerQuery(t, domain.Timeframe5m, "2026-09-11T10:00:00Z", "2026-09-11T10:10:00Z")
			p := testPlanner(t, query, 2, 8)
			row := finalRow(t, query, query.From)
			tc.change(&row)

			plan, err := p.Plan(query, []Stored{row}, query.To)

			assert.ErrorIs(t, err, application.ErrInternal)
			assert.Nil(t, plan)
		})
	}
}

func TestPlannerRejectsDuplicateCachedRows(t *testing.T) {
	cases := []struct {
		name      string
		duplicate func(Stored) Stored
	}{
		{"identical", func(row Stored) Stored { return row }},
		{"different evidence", func(row Stored) Stored {
			row.RequestStartedAt = row.Candle.OpenTime
			return row
		}},
		{"different timezone", func(row Stored) Stored {
			row.Candle.OpenTime = row.Candle.OpenTime.In(time.FixedZone("offset", 3600))
			return row
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := plannerQuery(t, domain.Timeframe5m, "2026-09-11T10:00:00Z", "2026-09-11T10:10:00Z")
			p := testPlanner(t, query, 2, 8)
			row := finalRow(t, query, query.From)

			plan, err := p.Plan(query, []Stored{row, tc.duplicate(row)}, query.To)

			assert.ErrorIs(t, err, application.ErrInternal)
			assert.Nil(t, plan)
		})
	}
}

func TestPlannerRechecksRetentionOnWarmCache(t *testing.T) {
	query := plannerQuery(t, domain.Timeframe5m, "2026-09-11T10:00:00Z", "2026-09-11T10:05:00Z")
	p := testPlanner(t, query, 1, 8)
	row := finalRow(t, query, query.From)
	require.NoError(t, p.Validate(query, query.To))

	plan, err := p.Plan(query, []Stored{row}, query.To.Add(5*time.Minute))

	assert.ErrorIs(t, err, application.ErrRangeOutOfRetention)
	assert.Nil(t, plan)
}

func TestPlannerRejectsOversizedQueryBeforeCachedRows(t *testing.T) {
	query := plannerQuery(t, domain.Timeframe1s, "1970-01-01T00:00:00Z", "2026-09-11T10:05:00Z")
	p := testPlanner(t, query, 1000, 8)

	plan, err := p.Plan(query, []Stored{{}}, query.To)

	assert.ErrorIs(t, err, application.ErrRequestTooLarge)
	assert.Nil(t, plan)
}

func TestPlannerRejectsExcessCachedRows(t *testing.T) {
	query := plannerQuery(t, domain.Timeframe5m, "2026-09-11T10:00:00Z", "2026-09-11T10:05:00Z")
	p := testPlanner(t, query, 1, 8)
	row := finalRow(t, query, query.From)

	plan, err := p.Plan(query, []Stored{row, row}, query.To)

	assert.ErrorIs(t, err, application.ErrInternal)
	assert.Nil(t, plan)
}

func TestPlannerCopiesSupportList(t *testing.T) {
	query := plannerQuery(t, domain.Timeframe5m, "2026-09-11T10:00:00Z", "2026-09-11T10:05:00Z")
	supported := []domain.Timeframe{domain.Timeframe5m}
	p, err := NewPlanner(query.Scope, supported, 1, 8)
	require.NoError(t, err)
	supported[0] = domain.Timeframe1m

	plan, err := p.Plan(query, nil, query.To)

	require.NoError(t, err)
	assert.Equal(t, []Request{{Query: query, Limit: 1}}, plan)
}

func TestPlannerConstructorValidation(t *testing.T) {
	cases := []struct {
		name      string
		exchange  domain.Exchange
		market    domain.Market
		supported []domain.Timeframe
		history   int64
		limit     int
	}{
		{"invalid exchange", "other", domain.MarketSpot, []domain.Timeframe{domain.Timeframe1m}, 8, 8},
		{"invalid market", domain.ExchangeBinance, "inverse", []domain.Timeframe{domain.Timeframe1m}, 8, 8},
		{"empty support", domain.ExchangeBinance, domain.MarketSpot, nil, 8, 8},
		{"invalid interval", domain.ExchangeBinance, domain.MarketSpot, []domain.Timeframe{"60m"}, 8, 8},
		{"unknown calendar", domain.ExchangeBybit, domain.MarketSpot, []domain.Timeframe{domain.Timeframe3d}, 8, 8},
		{"zero history", domain.ExchangeBinance, domain.MarketSpot, []domain.Timeframe{domain.Timeframe1m}, 0, 8},
		{"negative history", domain.ExchangeBinance, domain.MarketSpot, []domain.Timeframe{domain.Timeframe1m}, -1, 8},
		{"zero limit", domain.ExchangeBinance, domain.MarketSpot, []domain.Timeframe{domain.Timeframe1m}, 8, 0},
		{"negative limit", domain.ExchangeBinance, domain.MarketSpot, []domain.Timeframe{domain.Timeframe1m}, 8, -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewPlanner(application.Scope{Exchange: tc.exchange, Market: tc.market}, tc.supported, tc.history, tc.limit)

			assert.ErrorIs(t, err, application.ErrInvalidParameter)
			assert.Nil(t, p)
		})
	}
}
