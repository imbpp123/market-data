package upstream

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
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
	s.catalogStart = started
	defer c.notify()
	fail := func(reason string) error {
		s.blocked = fmt.Errorf("invalid %s limit catalog: %s: %w", scope, reason, application.ErrUpstreamUnavailable)
		return s.blocked
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
		value.limit = percent(min(value.ceiling, limit.Limit), 100-c.settings.SafetyMarginPercent)
		if !s.feasible(value) {
			return fail("allowance cannot admit a permitted request")
		}
		updates[key] = value
	}
	now := c.clock.Now()
	for key, w := range updates {
		if _, exists := s.windows[key]; !exists && s.historySince.After(now.Add(-w.duration)) {
			s.cooldown = maxTime(s.cooldown, now.Add(w.duration))
		}
		s.windows[key] = w
	}
	s.blocked = nil
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
