package kline

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func plannerTime(t *testing.T, value string) time.Time {
	t.Helper()
	result, err := time.Parse(time.RFC3339Nano, value)
	require.NoError(t, err)
	return result
}

func plannerQuery(t *testing.T, interval domain.Timeframe, from, to string) Query {
	t.Helper()
	return Query{
		Series: Series{
			Scope:  application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot},
			Symbol: "BTCUSDT", Interval: interval,
		},
		From: plannerTime(t, from), To: plannerTime(t, to),
	}
}

func testPlanner(t *testing.T, query Query, history int64, limit int) *Planner {
	t.Helper()
	p, err := NewPlanner(query.Scope, []domain.Timeframe{query.Interval}, history, limit)
	require.NoError(t, err)
	return p
}

func finalRow(t *testing.T, query Query, open time.Time) Stored {
	t.Helper()
	calendar, err := domain.NewCalendar(query.Exchange, query.Market, query.Interval)
	require.NoError(t, err)
	closeTime, err := calendar.Next(open)
	require.NoError(t, err)
	return Stored{
		Candle: domain.Kline{
			Exchange: query.Exchange, Market: query.Market, Symbol: query.Symbol, Interval: query.Interval,
			OpenTime: open, CloseTime: closeTime, FetchedAt: closeTime.Add(time.Second),
		},
		RequestStartedAt: closeTime,
	}
}

func TestPlannerGapRanges(t *testing.T) {
	cases := []struct {
		name    string
		missing []int
		limit   int
		want    [][2]int
	}{
		{name: "complete cache", limit: 8},
		{name: "empty cache exact limit", missing: []int{0, 1, 2, 3, 4, 5, 6, 7}, limit: 8, want: [][2]int{{0, 8}}},
		{name: "empty cache one over limit", missing: []int{0, 1, 2, 3, 4, 5, 6, 7}, limit: 7, want: [][2]int{{0, 7}, {7, 8}}},
		{name: "beginning", missing: []int{0, 1}, limit: 8, want: [][2]int{{0, 2}}},
		{name: "middle", missing: []int{3, 4}, limit: 8, want: [][2]int{{3, 5}}},
		{name: "end", missing: []int{6, 7}, limit: 8, want: [][2]int{{6, 8}}},
		{name: "merged gaps", missing: []int{0, 2, 5, 7}, limit: 8, want: [][2]int{{0, 8}}},
		{name: "split gaps", missing: []int{0, 2, 5, 7}, limit: 3, want: [][2]int{{0, 3}, {5, 8}}},
		{name: "one slot pages", missing: []int{0, 2, 5, 7}, limit: 1, want: [][2]int{{0, 1}, {2, 3}, {5, 6}, {7, 8}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := plannerQuery(t, domain.Timeframe5m, "2026-09-11T10:00:00Z", "2026-09-11T10:40:00Z")
			p := testPlanner(t, query, 8, tc.limit)
			var cached []Stored
			for slot := 0; slot < 8; slot++ {
				if !slices.Contains(tc.missing, slot) {
					cached = append(cached, finalRow(t, query, query.From.Add(time.Duration(slot)*5*time.Minute)))
				}
			}
			slices.Reverse(cached)
			before := slices.Clone(cached)
			want := make([]Request, 0, len(tc.want))
			for _, span := range tc.want {
				want = append(want, Request{Query: Query{
					Series: query.Series,
					From:   query.From.Add(time.Duration(span[0]) * 5 * time.Minute),
					To:     query.From.Add(time.Duration(span[1]) * 5 * time.Minute),
				}, Limit: span[1] - span[0]})
			}

			plan, err := p.Plan(query, cached, query.To)

			require.NoError(t, err)
			assert.Equal(t, want, plan)
			assert.Equal(t, before, cached)
		})
	}
}

func TestPlannerSpecificationExample(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		spans [][2]string
		sizes []int
	}{
		{"exact limit", 8, [][2]string{{"10:30", "11:10"}}, []int{8}},
		{"larger limit", 1000, [][2]string{{"10:30", "11:10"}}, []int{8}},
		{"lower configured limit", 7, [][2]string{{"10:30", "11:05"}, {"11:05", "11:10"}}, []int{7, 1}},
		{"separate gaps", 2, [][2]string{{"10:30", "10:40"}, {"11:00", "11:10"}}, []int{2, 2}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := plannerQuery(t, domain.Timeframe5m, "2026-09-11T10:00:00Z", "2026-09-11T15:00:00Z")
			p := testPlanner(t, query, 60, tc.limit)
			var cached []Stored
			for slot := 0; slot < 60; slot++ {
				if !slices.Contains([]int{6, 7, 12, 13}, slot) {
					cached = append(cached, finalRow(t, query, query.From.Add(time.Duration(slot)*5*time.Minute)))
				}
			}
			want := make([]Request, 0, len(tc.spans))
			for i, span := range tc.spans {
				want = append(want, Request{Query: plannerQuery(t, query.Interval,
					"2026-09-11T"+span[0]+":00Z", "2026-09-11T"+span[1]+":00Z"), Limit: tc.sizes[i]})
			}

			plan, err := p.Plan(query, cached, query.To)

			require.NoError(t, err)
			assert.Equal(t, want, plan)
		})
	}
}

func TestPlannerFinalization(t *testing.T) {
	cases := []struct {
		name            string
		now, start, end string
		refresh         bool
	}{
		{"open candle", "10:03:00", "10:02:00", "10:02:01", true},
		{"local close alone", "10:06:00", "10:02:00", "10:02:01", true},
		{"response crosses close", "10:06:00", "10:04:59", "10:05:01", true},
		{"request at close", "10:06:00", "10:05:00", "10:05:01", false},
		{"request after close", "10:06:00", "10:05:01", "10:05:02", false},
		{"open slot always refreshes", "10:03:00", "10:05:00", "10:05:01", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := plannerQuery(t, domain.Timeframe5m, "2026-09-11T10:00:00Z", "2026-09-11T10:05:00Z")
			p := testPlanner(t, query, 2, 8)
			row := finalRow(t, query, query.From)
			row.RequestStartedAt = plannerTime(t, "2026-09-11T"+tc.start+"Z")
			row.Candle.FetchedAt = plannerTime(t, "2026-09-11T"+tc.end+"Z")

			plan, err := p.Plan(query, []Stored{row}, plannerTime(t, "2026-09-11T"+tc.now+"Z"))

			require.NoError(t, err)
			if tc.refresh {
				assert.Equal(t, []Request{{Query: query, Limit: 1}}, plan)
			} else {
				assert.Empty(t, plan)
			}
		})
	}
}

func TestPlannerCalendarRanges(t *testing.T) {
	cases := []struct {
		name     string
		exchange domain.Exchange
		market   domain.Market
		interval domain.Timeframe
		from, to string
		boundary string
	}{
		{"leap February", domain.ExchangeBinance, domain.MarketSpot, domain.Timeframe1M, "2028-01-01", "2028-04-01", "2028-03-01"},
		{"regular February", domain.ExchangeBybit, domain.MarketLinear, domain.Timeframe1M, "2027-01-01", "2027-04-01", "2027-03-01"},
		{"monthly year boundary", domain.ExchangeBinance, domain.MarketLinear, domain.Timeframe1M, "2027-11-01", "2028-02-01", "2028-01-01"},
		{"Binance spot week", domain.ExchangeBinance, domain.MarketSpot, domain.Timeframe1w, "2025-12-22", "2026-01-12", "2026-01-05"},
		{"Binance linear week", domain.ExchangeBinance, domain.MarketLinear, domain.Timeframe1w, "2025-12-22", "2026-01-12", "2026-01-05"},
		{"Bybit spot week", domain.ExchangeBybit, domain.MarketSpot, domain.Timeframe1w, "2025-12-22", "2026-01-12", "2026-01-05"},
		{"Bybit linear week", domain.ExchangeBybit, domain.MarketLinear, domain.Timeframe1w, "2025-12-22", "2026-01-12", "2026-01-05"},
		{"Binance spot three days", domain.ExchangeBinance, domain.MarketSpot, domain.Timeframe3d, "2025-12-27", "2026-01-05", "2026-01-02"},
		{"Binance linear three days", domain.ExchangeBinance, domain.MarketLinear, domain.Timeframe3d, "2025-12-27", "2026-01-05", "2026-01-02"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := plannerQuery(t, tc.interval, tc.from+"T00:00:00Z", tc.to+"T00:00:00Z")
			query.Exchange, query.Market = tc.exchange, tc.market
			p := testPlanner(t, query, 3, 2)
			middle := plannerTime(t, tc.boundary+"T00:00:00Z")

			plan, err := p.Plan(query, nil, query.To)

			require.NoError(t, err)
			assert.Equal(t, []Request{
				{Query: Query{Series: query.Series, From: query.From, To: middle}, Limit: 2},
				{Query: Query{Series: query.Series, From: middle, To: query.To}, Limit: 1},
			}, plan)
		})
	}
}

func TestPlannerMergesMonthlyMissingAndIntermediateSlots(t *testing.T) {
	query := plannerQuery(t, domain.Timeframe1M, "2027-12-01T00:00:00Z", "2028-05-01T00:00:00Z")
	p := testPlanner(t, query, 5, 4)
	intermediate := finalRow(t, query, plannerTime(t, "2028-03-01T00:00:00Z"))
	intermediate.RequestStartedAt = intermediate.Candle.CloseTime.Add(-time.Second)
	cached := []Stored{
		finalRow(t, query, query.From),
		finalRow(t, query, plannerTime(t, "2028-02-01T00:00:00Z")),
		intermediate,
		finalRow(t, query, plannerTime(t, "2028-04-01T00:00:00Z")),
	}

	plan, err := p.Plan(query, cached, query.To)

	require.NoError(t, err)
	assert.Equal(t, []Request{{Query: plannerQuery(t, domain.Timeframe1M,
		"2028-01-01T00:00:00Z", "2028-04-01T00:00:00Z"), Limit: 3}}, plan)
}

func TestPlannerCoversEveryGapWithMinimumRequests(t *testing.T) {
	const slots = 6
	cases := make([]struct {
		name    string
		missing int
		limit   int
	}, 0)
	for missing := 0; missing < 1<<slots; missing++ {
		for limit := 1; limit <= slots; limit++ {
			cases = append(cases, struct {
				name    string
				missing int
				limit   int
			}{fmt.Sprintf("missing_%06b_limit_%d", missing, limit), missing, limit})
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := plannerQuery(t, domain.Timeframe1m, "2026-09-11T10:00:00Z", "2026-09-11T10:06:00Z")
			p := testPlanner(t, query, slots, tc.limit)
			var cached []Stored
			for slot := 0; slot < slots; slot++ {
				if tc.missing&(1<<slot) == 0 {
					cached = append(cached, finalRow(t, query, query.From.Add(time.Duration(slot)*time.Minute)))
				}
			}

			plan, err := p.Plan(query, cached, query.To)

			require.NoError(t, err)
			covered := 0
			previous := query.From
			for _, request := range plan {
				assert.Equal(t, query.Series, request.Series)
				assert.False(t, request.From.Before(previous))
				assert.False(t, request.To.After(query.To))
				assert.Greater(t, request.Limit, 0)
				assert.LessOrEqual(t, request.Limit, tc.limit)
				assert.Equal(t, time.Duration(request.Limit)*time.Minute, request.To.Sub(request.From))
				for slot := 0; slot < slots; slot++ {
					open := query.From.Add(time.Duration(slot) * time.Minute)
					if !open.Before(request.From) && open.Before(request.To) {
						covered |= 1 << slot
					}
				}
				previous = request.To
			}
			assert.Equal(t, tc.missing, covered&tc.missing)
			assert.Len(t, plan, minimumPageCount(tc.missing, tc.limit, slots))
		})
	}
}

// Exhaust all possible page placements and covered subsets for a small window.
// This oracle does not use the planner's earliest-gap greedy rule.
func minimumPageCount(missing, limit, slots int) int {
	cost := make([]int, 1<<slots)
	for mask := 1; mask < len(cost); mask++ {
		cost[mask] = slots + 1
	}
	for covered := 0; covered < len(cost); covered++ {
		for from := 0; from < slots; from++ {
			page := 0
			for to := from; to < slots && to-from < limit; to++ {
				page |= 1 << to
				next := covered | (page & missing)
				cost[next] = min(cost[next], cost[covered]+1)
			}
		}
	}
	return cost[missing]
}
