package upstream

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"market-data/internal/application"
)

type catalogLimit struct {
	Type     string `json:"rateLimitType"`
	Interval string `json:"interval"`
	Number   int64  `json:"intervalNum"`
	Limit    int    `json:"limit"`
}

func (c *Controller) updateCatalog(scope Scope, started time.Time, body []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.scopes[scope]
	if started.Before(s.catalogStart) {
		return nil
	}
	defer c.notify()
	fail := func(reason string) error {
		return fmt.Errorf("invalid %s limit catalog: %s: %w", scope, reason, application.ErrUpstreamUnavailable)
	}
	var catalog struct {
		Limits *[]catalogLimit `json:"rateLimits"`
	}
	if json.Unmarshal(body, &catalog) != nil || catalog.Limits == nil {
		return fail("missing or malformed rateLimits")
	}
	updates := make(map[string]window)
	for _, limit := range *catalog.Limits {
		if limit.Type == "ORDERS" {
			continue
		}
		unit := ""
		switch limit.Type {
		case "REQUEST_WEIGHT":
			unit = "weight"
		case "RAW_REQUESTS":
			unit = "requests"
		default:
			return fail("unknown applicable limit type")
		}
		duration := map[string]time.Duration{"SECOND": time.Second, "MINUTE": time.Minute, "HOUR": time.Hour, "DAY": 24 * time.Hour}[limit.Interval]
		if duration == 0 || limit.Number <= 0 || limit.Number > math.MaxInt64/int64(duration) || limit.Limit <= 0 {
			return fail("invalid limit interval or amount")
		}
		duration *= time.Duration(limit.Number)
		key := unit + "_" + strconv.FormatInt(int64(duration), 10)
		value := window{unit: unit, duration: duration, ceiling: limit.Limit, split: true}
		for existing, w := range s.windows {
			if !w.funding && w.unit == unit && w.duration == duration {
				key = existing
				value = w
				break
			}
		}
		if _, duplicate := updates[key]; duplicate {
			return fail("duplicate applicable window")
		}
		value.ceiling = limit.Limit
		value.source = "exchange_info"
		value.updatedAt = c.clock.Now()
		effective := limit.Limit
		if value.userCap > 0 {
			effective = min(effective, value.userCap)
		}
		value.limit = percent(effective, c.settings.Binance.StopThresholdPercent)
		// A valid reduction applies even when some operations no longer fit.
		// Admission checks each request against its new allowance.
		updates[key] = value
	}
	var changed []LimitState
	for key, w := range updates {
		old, exists := s.windows[key]
		if !exists || old.ceiling != w.ceiling || old.source != w.source {
			changed = append(changed, LimitState{Name: key, Unit: w.unit, Window: w.duration, ExchangeLimit: w.ceiling, UserCap: w.userCap, StopLine: w.limit, Source: w.source, UpdatedAt: w.updatedAt, HistorySince: s.historySince})
		}
		s.windows[key] = w
	}
	s.catalogStart = started
	s.catalogUpdatedAt = c.clock.Now()
	recovered := s.catalogError != "" && !started.Before(s.catalogErrorStart)
	if !started.Before(s.catalogErrorStart) {
		s.catalogError, s.catalogErrorAt, s.catalogErrorStart = "", time.Time{}, time.Time{}
	}
	if len(changed) > 0 {
		slices.SortFunc(changed, func(a, b LimitState) int { return strings.Compare(a.Name, b.Name) })
		c.diagnostic(DiagnosticEvent{Scope: scope, Reason: "catalog_changed", Limits: changed, ThresholdPercent: c.settings.Binance.StopThresholdPercent})
	} else if recovered {
		c.diagnostic(DiagnosticEvent{Scope: scope, Reason: "catalog_recovered"})
	}
	return nil
}

func (s *scopeState) feasible(w window) bool {
	if w.funding {
		return w.limit >= 1
	}
	for op, share := range s.shares {
		if share == 0 {
			continue
		}
		amount := 1
		if w.unit == "weight" {
			amount = s.maxCosts[op]
		}
		if amount > w.limit || w.split && amount > percent(w.limit, share) {
			return false
		}
	}
	return true
}

// LimitState describes an installed window. A zero UserCap means no user cap.
// HistorySince marks the oldest available local history, not known exchange usage.
type LimitState struct {
	Name          string
	Unit          string
	Window        time.Duration
	ExchangeLimit int
	UserCap       int
	StopLine      int
	Source        string
	UpdatedAt     time.Time
	HistorySince  time.Time
}

// Limits returns an independent snapshot for logs and diagnostics.
func (c *Controller) Limits(scope Scope) []LimitState {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := c.scopes[scope]
	if state == nil {
		return nil
	}
	result := make([]LimitState, 0, len(state.windows))
	for name, w := range state.windows {
		result = append(result, LimitState{Name: name, Unit: w.unit, Window: w.duration, ExchangeLimit: w.ceiling, UserCap: w.userCap, StopLine: w.limit, Source: w.source, UpdatedAt: w.updatedAt, HistorySince: state.historySince})
	}
	slices.SortFunc(result, func(a, b LimitState) int { return strings.Compare(a.Name, b.Name) })
	return result
}
