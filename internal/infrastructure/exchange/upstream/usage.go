package upstream

import (
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

type usageObservation struct {
	used    int
	expires time.Time
}

type usageView struct {
	common            int
	local             int
	observed          int
	reserved          int
	operations        [operationCount]int
	next              time.Time
	uncertain         bool
	incompleteHistory bool
}

func (s *scopeState) prune(now time.Time) {
	longest := time.Duration(0)
	for _, w := range s.windows {
		longest = max(longest, w.duration)
	}
	cutoff := now.Add(-longest)
	s.history = slices.DeleteFunc(s.history, func(e *entry) bool {
		for duration, observation := range e.observations {
			if !observation.expires.After(now) {
				delete(e.observations, duration)
			}
		}
		return !e.inflight && !e.at.After(cutoff) && len(e.observations) == 0
	})
	if len(s.history) == 0 {
		s.history = nil
	}
	if cutoff.After(s.historySince) {
		s.historySince = cutoff
	}
}

func (s *scopeState) live(e *entry, w window, now time.Time) bool {
	return s.keepInflight && e.inflight || e.at.Add(w.duration).After(now)
}

func (s *scopeState) usage(w window, now time.Time) usageView {
	result := usageView{incompleteHistory: s.historySince.After(now.Add(-w.duration))}
	for _, e := range s.history {
		if !s.live(e, w, now) {
			continue
		}
		amount := w.cost(e.cost)
		result.local = addUsage(result.local, amount)
		result.operations[e.cost.operation] = addUsage(result.operations[e.cost.operation], amount)
		if e.inflight {
			result.reserved = addUsage(result.reserved, amount)
		}
		if amount > 0 && (!e.inflight || !s.keepInflight) {
			result.next = earlier(result.next, e.at.Add(w.duration))
		}
	}
	result.common = result.local
	if w.unit == "weight" {
		for _, e := range s.history {
			observation, ok := e.observations[w.duration]
			if !ok || !observation.expires.After(now) {
				continue
			}
			unmatched := result.local
			if s.live(e, w, now) && unmatched != math.MaxInt {
				unmatched -= w.cost(e.cost)
			}
			// Only the response's own attempt is proven included. Without a
			// server window identity, other attempts may belong to another window.
			result.common = max(result.common, addUsage(observation.used, unmatched))
			result.observed = max(result.observed, observation.used)
			result.next = earlier(result.next, observation.expires)
			result.uncertain = true
		}
		// Missing or invalid headers do not confirm external IP usage either.
		result.uncertain = result.uncertain || result.local > 0
	}
	result.uncertain = result.uncertain || result.incompleteHistory
	return result
}

func earlier(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}

func addUsage(a, b int) int {
	if b > math.MaxInt-a {
		return math.MaxInt
	}
	return a + b
}

func (c *Controller) usageHeaders(scope Scope, path string, headers http.Header, attempt *entry) {
	if scope == BinanceLinear && (path == "/fapi/v2/ticker/price" || path == "/fapi/v1/ticker/bookTicker") {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.scopes[scope]
	now := c.clock.Now()
	s.prune(now)
	if attempt.received.IsZero() {
		attempt.received = now
	}
	for _, w := range s.windows {
		if w.unit != "weight" {
			continue
		}
		suffix := windowSuffix(w.duration)
		if suffix == "" {
			continue
		}
		used, valid := usedWeight(headers, "X-Mbx-Used-Weight-"+suffix)
		if !valid || !attempt.dispatched || used < w.cost(attempt.cost) {
			continue
		}
		if attempt.observations == nil {
			attempt.observations = make(map[time.Duration]usageObservation)
		}
		// These responses do not provide a reliable exchange window identity.
		// Preserve Go's monotonic clock and use a full window after receipt.
		attempt.observations[w.duration] = usageObservation{used: used, expires: attempt.received.Add(w.duration)}
	}
	c.notify()
}

func usedWeight(headers http.Header, name string) (int, bool) {
	used, found := 0, false
	for key, values := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		for _, value := range values {
			for _, part := range strings.Split(value, ",") {
				part = strings.TrimSpace(part)
				if part == "" || strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
					return 0, false
				}
				amount, err := strconv.Atoi(part)
				if err != nil || found && amount != used {
					return 0, false
				}
				used, found = amount, true
			}
		}
	}
	return used, found
}
