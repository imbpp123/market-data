package domain

import (
	"errors"
	"time"
)

type Timeframe string

const (
	Timeframe1s  Timeframe = "1s"
	Timeframe1m  Timeframe = "1m"
	Timeframe3m  Timeframe = "3m"
	Timeframe5m  Timeframe = "5m"
	Timeframe15m Timeframe = "15m"
	Timeframe30m Timeframe = "30m"
	Timeframe1h  Timeframe = "1h"
	Timeframe2h  Timeframe = "2h"
	Timeframe4h  Timeframe = "4h"
	Timeframe6h  Timeframe = "6h"
	Timeframe8h  Timeframe = "8h"
	Timeframe12h Timeframe = "12h"
	Timeframe1d  Timeframe = "1d"
	Timeframe3d  Timeframe = "3d"
	Timeframe1w  Timeframe = "1w"
	Timeframe1M  Timeframe = "1M"
)

var (
	ErrInvalidTimeframe = errors.New("invalid timeframe")
	ErrUnknownCalendar  = errors.New("calendar alignment is not confirmed")
	ErrInvalidBoundary  = errors.New("time is not a slot boundary")
	ErrTimeOverflow     = errors.New("time is outside calendar years 1..9999")
	ErrInvalidRange     = errors.New("invalid slot range")
	ErrSlotLimit        = errors.New("slot count exceeds limit")
)

func ParseTimeframe(value string) (Timeframe, error) {
	interval := Timeframe(value)
	if !interval.Valid() {
		return "", ErrInvalidTimeframe
	}

	return interval, nil
}

func (t Timeframe) Valid() bool {
	return t == Timeframe1M || t.seconds() > 0
}

func (t Timeframe) seconds() int64 {
	switch t {
	case Timeframe1s:
		return 1
	case Timeframe1m:
		return 60
	case Timeframe3m:
		return 180
	case Timeframe5m:
		return 300
	case Timeframe15m:
		return 900
	case Timeframe30m:
		return 1800
	case Timeframe1h:
		return 3600
	case Timeframe2h:
		return 7200
	case Timeframe4h:
		return 14400
	case Timeframe6h:
		return 21600
	case Timeframe8h:
		return 28800
	case Timeframe12h:
		return 43200
	case Timeframe1d:
		return 86400
	case Timeframe3d:
		return 259200
	case Timeframe1w:
		return 604800
	default:
		return 0
	}
}

// Calendar defines alignment, not endpoint support. The provider must also
// accept the interval. Its zero value is invalid.
type Calendar struct {
	interval Timeframe
	anchor   int64
}

func NewCalendar(exchange Exchange, market Market, interval Timeframe) (Calendar, error) {
	if !interval.Valid() {
		return Calendar{}, ErrInvalidTimeframe
	}

	if !exchange.Valid() || !market.Valid() {
		return Calendar{}, ErrUnknownCalendar
	}

	c := Calendar{interval: interval}
	switch interval {
	case Timeframe3d:
		if exchange != ExchangeBinance {
			return Calendar{}, ErrUnknownCalendar
		}

		c.anchor = 86400 // January 2, 1970 UTC, confirmed by captured rows.
	case Timeframe1w:
		c.anchor = 4 * 86400 // Monday, January 5, 1970 UTC.
	}

	return c, nil
}

// Floor returns the latest boundary at or before t, in UTC.
func (c Calendar) Floor(t time.Time) (time.Time, error) {
	if !c.interval.Valid() {
		return time.Time{}, ErrInvalidTimeframe
	}

	t = t.UTC()
	if t.Year() < 1 || t.Year() > 9999 {
		return time.Time{}, ErrTimeOverflow
	}

	if c.interval == Timeframe1M {
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC), nil
	}

	seconds := c.interval.seconds()
	remainder := (t.Unix() - c.anchor) % seconds
	if remainder < 0 {
		remainder += seconds
	}

	result := time.Unix(t.Unix()-remainder, 0).UTC()
	if result.Year() < 1 {
		return time.Time{}, ErrTimeOverflow
	}

	return result, nil
}

func (c Calendar) ValidateBoundary(t time.Time) error {
	boundary, err := c.Floor(t)
	if err != nil {
		return err
	}

	if !boundary.Equal(t) {
		return ErrInvalidBoundary
	}

	return nil
}

// Shift moves an aligned boundary by signed slots without duration saturation.
// Years 1..9999 allow pre-epoch retention cutoffs; public request validation
// separately rejects pre-epoch timestamps.
func (c Calendar) Shift(boundary time.Time, slots int64) (time.Time, error) {
	if err := c.ValidateBoundary(boundary); err != nil {
		return time.Time{}, err
	}

	boundary = boundary.UTC()
	if c.interval == Timeframe1M {
		month := int64(boundary.Year()-1)*12 + int64(boundary.Month()-1)
		if slots < -month || slots > 9999*12-1-month {
			return time.Time{}, ErrTimeOverflow
		}

		month += slots
		return time.Date(int(month/12)+1, time.Month(month%12)+1, 1, 0, 0, 0, 0, time.UTC), nil
	}

	const minSeconds int64 = -62135596800 // January 1, year 1.
	const maxSeconds int64 = 253402300799 // December 31, year 9999, 23:59:59.
	seconds := c.interval.seconds()
	start := boundary.Unix()
	if slots < (minSeconds-start)/seconds || slots > (maxSeconds-start)/seconds {
		return time.Time{}, ErrTimeOverflow
	}

	return time.Unix(start+slots*seconds, 0).UTC(), nil
}

func (c Calendar) Next(boundary time.Time) (time.Time, error) {
	return c.Shift(boundary, 1)
}

// CountSlots validates both half-open boundaries and the bound before any
// allocation. Oversized ranges return no partial count.
func (c Calendar) CountSlots(from, to time.Time, limit int64) (int64, error) {
	if err := c.ValidateBoundary(from); err != nil {
		return 0, err
	}

	if err := c.ValidateBoundary(to); err != nil {
		return 0, err
	}

	if limit < 0 || from.After(to) {
		return 0, ErrInvalidRange
	}

	from, to = from.UTC(), to.UTC()
	var count int64
	if c.interval == Timeframe1M {
		count = int64(to.Year()-from.Year())*12 + int64(to.Month()-from.Month())
	} else {
		count = (to.Unix() - from.Unix()) / c.interval.seconds()
	}

	if count > limit {
		return 0, ErrSlotLimit
	}

	return count, nil
}

func (c Calendar) HistoryCutoff(now time.Time, slots int64) (time.Time, error) {
	if slots <= 0 {
		return time.Time{}, ErrInvalidRange
	}

	current, err := c.Floor(now)
	if err != nil {
		return time.Time{}, err
	}

	return c.Shift(current, -slots)
}
