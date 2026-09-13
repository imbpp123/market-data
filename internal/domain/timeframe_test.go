package domain

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func instant(t *testing.T, value string) time.Time {
	t.Helper()
	result, err := time.Parse(time.RFC3339Nano, value)
	require.NoError(t, err)
	return result
}

func calendar(t *testing.T, interval Timeframe) Calendar {
	t.Helper()
	result, err := NewCalendar(ExchangeBinance, MarketSpot, interval)
	require.NoError(t, err)
	return result
}

func TestTimeframeAllowlist(t *testing.T) {
	for _, value := range []string{
		"1s", "1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w", "1M",
	} {
		t.Run(value, func(t *testing.T) {
			result, err := ParseTimeframe(value)
			require.NoError(t, err)
			assert.Equal(t, Timeframe(value), result)
			assert.True(t, result.Valid())
		})
	}

	for _, value := range []string{"", "0", "0m", "60m", "24h", "2d", "30d", "1H", "1W", "1Y", " 1m", "1m ", "1m\n", "\t", "1m\x00", "unknown"} {
		t.Run("invalid_"+value, func(t *testing.T) {
			result, err := ParseTimeframe(value)
			assert.ErrorIs(t, err, ErrInvalidTimeframe)
			assert.Empty(t, result)
			assert.False(t, Timeframe(value).Valid())
			_, err = NewCalendar(ExchangeBinance, MarketSpot, Timeframe(value))
			assert.ErrorIs(t, err, ErrInvalidTimeframe)
		})
	}
}

func TestFixedSlotsAndUTC(t *testing.T) {
	cases := []struct {
		interval Timeframe
		seconds  int64
	}{
		{Timeframe1s, 1}, {Timeframe1m, 60}, {Timeframe3m, 180}, {Timeframe5m, 300},
		{Timeframe15m, 900}, {Timeframe30m, 1800}, {Timeframe1h, 3600}, {Timeframe2h, 7200},
		{Timeframe4h, 14400}, {Timeframe6h, 21600}, {Timeframe8h, 28800}, {Timeframe12h, 43200},
		{Timeframe1d, 86400}, {Timeframe3d, 259200}, {Timeframe1w, 604800},
	}
	for _, tc := range cases {
		t.Run(string(tc.interval), func(t *testing.T) {
			c := calendar(t, tc.interval)
			// January 5 is both a Monday and a confirmed Binance 3d start.
			start := instant(t, "2026-01-05T03:00:00+03:00")
			next, err := c.Next(start)
			require.NoError(t, err)
			assert.Equal(t, start.Unix()+tc.seconds, next.Unix())
			assert.Same(t, time.UTC, next.Location())
			previous, err := c.Shift(next, -1)
			require.NoError(t, err)
			assert.True(t, previous.Equal(start))
			floor, err := c.Floor(next.Add(-time.Nanosecond))
			require.NoError(t, err)
			assert.Equal(t, previous, floor)
			assert.ErrorIs(t, c.ValidateBoundary(start.Add(time.Nanosecond)), ErrInvalidBoundary)
		})
	}
}

func TestCalendarTransitions(t *testing.T) {
	cases := []struct {
		interval Timeframe
		from, to string
	}{
		{Timeframe1m, "2025-12-31T23:59:00Z", "2026-01-01T00:00:00Z"},
		{Timeframe1d, "2028-02-28T00:00:00Z", "2028-02-29T00:00:00Z"},
		{Timeframe1d, "2028-02-29T00:00:00Z", "2028-03-01T00:00:00Z"},
		{Timeframe1M, "2027-02-01T00:00:00Z", "2027-03-01T00:00:00Z"},
		{Timeframe1M, "2028-02-01T00:00:00Z", "2028-03-01T00:00:00Z"},
		{Timeframe1M, "2028-04-01T00:00:00Z", "2028-05-01T00:00:00Z"},
		{Timeframe1M, "2028-01-01T00:00:00Z", "2028-02-01T00:00:00Z"},
		{Timeframe1M, "2028-12-01T00:00:00Z", "2029-01-01T00:00:00Z"},
		{Timeframe1M, "2100-02-01T00:00:00Z", "2100-03-01T00:00:00Z"},
		{Timeframe1w, "2025-12-29T00:00:00Z", "2026-01-05T00:00:00Z"},
		{Timeframe3d, "2025-12-30T00:00:00Z", "2026-01-02T00:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(string(tc.interval)+tc.from, func(t *testing.T) {
			c := calendar(t, tc.interval)
			from, to := instant(t, tc.from), instant(t, tc.to)
			next, err := c.Next(from)
			require.NoError(t, err)
			assert.Equal(t, to, next)
			previous, err := c.Shift(to, -1)
			require.NoError(t, err)
			assert.Equal(t, from, previous)
			floor, err := c.Floor(to.Add(-time.Nanosecond))
			require.NoError(t, err)
			assert.Equal(t, from, floor)
		})
	}

	c := calendar(t, Timeframe1M)
	floor, err := c.Floor(instant(t, "2028-03-01T01:00:00+02:00"))
	require.NoError(t, err)
	assert.Equal(t, instant(t, "2028-02-01T00:00:00Z"), floor)
	assert.ErrorIs(t, c.ValidateBoundary(instant(t, "2028-02-29T00:00:00Z")), ErrInvalidBoundary)
}

func TestConfirmedCalendarFixtures(t *testing.T) {
	files, err := filepath.Glob("../../testdata/exchange/*-*-*USDT-*.json")
	require.NoError(t, err)
	require.Len(t, files, 12)
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			require.NoError(t, err)
			var fixture struct {
				Exchange Exchange  `json:"exchange"`
				Market   Market    `json:"market"`
				Interval Timeframe `json:"interval"`
				Rows     []struct {
					Open  time.Time `json:"open_time"`
					Close time.Time `json:"close_time"`
				} `json:"expected_rows"`
			}
			require.NoError(t, json.Unmarshal(data, &fixture))
			require.NotEmpty(t, fixture.Rows)
			c, err := NewCalendar(fixture.Exchange, fixture.Market, fixture.Interval)
			require.NoError(t, err)
			for _, row := range fixture.Rows {
				require.NoError(t, c.ValidateBoundary(row.Open))
				next, err := c.Next(row.Open)
				require.NoError(t, err)
				assert.Equal(t, row.Close, next)
			}
		})
	}
}

func TestCalendarGatesAndAnchors(t *testing.T) {
	for _, scope := range []struct {
		exchange Exchange
		market   Market
	}{
		{"", MarketSpot}, {"other", MarketSpot}, {ExchangeBinance, "inverse"}, {ExchangeBybit, ""},
	} {
		_, err := NewCalendar(scope.exchange, scope.market, Timeframe1m)
		assert.ErrorIs(t, err, ErrUnknownCalendar)
	}
	for _, market := range []Market{MarketSpot, MarketLinear} {
		_, err := NewCalendar(ExchangeBybit, market, Timeframe3d)
		assert.ErrorIs(t, err, ErrUnknownCalendar)
	}

	c := calendar(t, Timeframe3d)
	assert.ErrorIs(t, c.ValidateBoundary(instant(t, "1970-01-01T00:00:00Z")), ErrInvalidBoundary)
	floor, err := c.Floor(instant(t, "1970-01-01T00:00:00Z"))
	require.NoError(t, err)
	assert.Equal(t, instant(t, "1969-12-30T00:00:00Z"), floor)
	c = calendar(t, Timeframe1w)
	floor, err = c.Floor(instant(t, "1970-01-01T00:00:00Z"))
	require.NoError(t, err)
	assert.Equal(t, instant(t, "1969-12-29T00:00:00Z"), floor)
	assert.ErrorIs(t, c.ValidateBoundary(instant(t, "2026-01-04T00:00:00Z")), ErrInvalidBoundary)
}

func TestSlotCountAndHistoryCutoff(t *testing.T) {
	cases := []struct {
		interval        Timeframe
		from, to, extra string
	}{
		{Timeframe1m, "2026-01-01T00:00:00Z", "2026-01-01T16:40:00Z", "2026-01-01T16:41:00Z"},
		{Timeframe1M, "1943-09-01T00:00:00Z", "2027-01-01T00:00:00Z", "2027-02-01T00:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(string(tc.interval), func(t *testing.T) {
			c := calendar(t, tc.interval)
			from, to, extra := instant(t, tc.from), instant(t, tc.to), instant(t, tc.extra)
			count, err := c.CountSlots(from, to, 1000)
			require.NoError(t, err)
			assert.EqualValues(t, 1000, count)
			count, err = c.CountSlots(from, extra, 1000)
			assert.ErrorIs(t, err, ErrSlotLimit)
			assert.Zero(t, count)
			count, err = c.CountSlots(from, from, 0)
			require.NoError(t, err)
			assert.Zero(t, count)
			cutoff, err := c.HistoryCutoff(to.Add(time.Second), 1000)
			require.NoError(t, err)
			assert.Equal(t, from, cutoff)
			assert.True(t, from.Add(-time.Nanosecond).Before(cutoff))
			cutoff, err = c.HistoryCutoff(extra, 1000)
			require.NoError(t, err)
			assert.True(t, from.Before(cutoff))
		})
	}

	c := calendar(t, Timeframe1M)
	cutoff, err := c.HistoryCutoff(instant(t, "2028-03-20T00:00:00Z"), 1)
	require.NoError(t, err)
	assert.Equal(t, instant(t, "2028-02-01T00:00:00Z"), cutoff)
	count, err := c.CountSlots(instant(t, "0001-01-01T00:00:00Z"), instant(t, "9999-12-01T00:00:00Z"), math.MaxInt64)
	require.NoError(t, err)
	assert.EqualValues(t, 119987, count)
	c = calendar(t, Timeframe1s)
	count, err = c.CountSlots(instant(t, "0001-01-01T00:00:00Z"), instant(t, "9999-12-31T23:59:59Z"), math.MaxInt64)
	require.NoError(t, err)
	assert.EqualValues(t, 315537897599, count)
}

func TestCalendarErrors(t *testing.T) {
	start := instant(t, "2026-01-01T00:00:00Z")
	for _, interval := range []Timeframe{Timeframe1s, Timeframe1M} {
		c := calendar(t, interval)
		for _, slots := range []int64{math.MaxInt64, math.MinInt64} {
			_, err := c.Shift(start, slots)
			assert.ErrorIs(t, err, ErrTimeOverflow)
		}
		for _, year := range []int{0, 10000} {
			_, err := c.Floor(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC))
			assert.ErrorIs(t, err, ErrTimeOverflow)
		}
		_, err := c.Shift(start.Add(time.Nanosecond), 0)
		assert.ErrorIs(t, err, ErrInvalidBoundary)
		_, err = c.Shift(instant(t, "0001-01-01T00:00:00Z"), -1)
		assert.ErrorIs(t, err, ErrTimeOverflow)
		end := instant(t, "9999-12-31T23:59:59Z")
		if interval == Timeframe1M {
			end = instant(t, "9999-12-01T00:00:00Z")
		}
		_, err = c.Next(end)
		assert.ErrorIs(t, err, ErrTimeOverflow)
		for _, pair := range [][2]time.Time{{start.Add(time.Nanosecond), start}, {start, start.Add(time.Nanosecond)}} {
			_, err = c.CountSlots(pair[0], pair[1], 10)
			assert.ErrorIs(t, err, ErrInvalidBoundary)
		}
		_, err = c.CountSlots(start, start, -1)
		assert.ErrorIs(t, err, ErrInvalidRange)
		next, err := c.Next(start)
		require.NoError(t, err)
		_, err = c.CountSlots(next, start, 10)
		assert.ErrorIs(t, err, ErrInvalidRange)
		for _, slots := range []int64{0, -1, math.MinInt64} {
			_, err = c.HistoryCutoff(start, slots)
			assert.ErrorIs(t, err, ErrInvalidRange)
		}
		_, err = c.HistoryCutoff(start, math.MaxInt64)
		assert.ErrorIs(t, err, ErrTimeOverflow)
	}
	var zero Calendar
	_, err := zero.Floor(start)
	assert.ErrorIs(t, err, ErrInvalidTimeframe)
	_, err = zero.HistoryCutoff(start, 1)
	assert.ErrorIs(t, err, ErrInvalidTimeframe)
}
