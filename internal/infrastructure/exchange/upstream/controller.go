package upstream

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
)

type window struct {
	unit      string
	duration  time.Duration
	ceiling   int
	userCap   int
	source    string
	updatedAt time.Time
	limit     int
	split     bool
	funding   bool
}

type entry struct {
	at   time.Time
	cost cost
}

type scopeState struct {
	windows          map[string]window
	history          []entry
	historySince     time.Time
	spacing          time.Duration
	next             time.Time
	cooldown         time.Time
	catalogUpdatedAt time.Time
	catalogStart     time.Time
	shares           [operationCount]int
	maxCosts         [operationCount]int
}

type lane struct {
	slots    int
	capacity int
	active   int
	queue    []*waiter
}

type waiter struct {
	ctx       context.Context
	scope     Scope
	cost      cost
	operation *operationState
	expires   time.Time
	notBefore time.Time
}

type Controller struct {
	mu           sync.Mutex
	clock        Clock
	settings     config.Upstream
	scopes       map[Scope]*scopeState
	lanes        [2 * operationCount]lane
	nextLane     int
	active       int
	queued       int
	changed      chan struct{}
	cycleTimeout [operationCount]time.Duration
	attempts     [operationCount]int
}

// New creates fresh in-memory ledgers. It makes no upstream requests.
func New(cfg config.Config, clock Clock) (*Controller, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if clock == nil {
		return nil, fmt.Errorf("upstream clock is required")
	}
	c := &Controller{clock: clock, settings: cfg.Upstream, scopes: make(map[Scope]*scopeState), changed: make(chan struct{})}
	l := cfg.Upstream.LanesPerExchange
	c.cycleTimeout = [operationCount]time.Duration{l.Tickers.CycleTimeout, cfg.Klines.FillTimeout, l.Instruments.CycleTimeout, l.MarketStats.CycleTimeout}
	c.attempts = [operationCount]int{l.Tickers.MaxAttempts, l.Klines.MaxAttempts, l.Instruments.MaxAttempts, l.MarketStats.MaxAttempts}
	lanes := []config.Lane{{HTTPSlots: l.Tickers.HTTPSlots, Waiters: l.Tickers.Waiters}, l.Klines, {HTTPSlots: l.Instruments.HTTPSlots, Waiters: l.Instruments.Waiters}, {HTTPSlots: l.MarketStats.HTTPSlots, Waiters: l.MarketStats.Waiters}}
	for i := range c.lanes {
		c.lanes[i] = lane{slots: lanes[i%int(operationCount)].HTTPSlots, capacity: lanes[i%int(operationCount)].Waiters}
	}
	if cfg.Exchanges.Binance.Enabled {
		if slices.Contains(cfg.Exchanges.Binance.Markets, "spot") {
			c.addScope(BinanceSpot, cfg.Upstream.Limits.BinanceSpot, [operationCount]int{4, 2, 20, 80})
		}
		if slices.Contains(cfg.Exchanges.Binance.Markets, "linear") {
			maximum := cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Linear
			weight := 10
			switch {
			case maximum < 100:
				weight = 1
			case maximum < 500:
				weight = 2
			case maximum <= 1000:
				weight = 5
			}
			c.addScope(BinanceLinear, cfg.Upstream.Limits.BinanceLinear, [operationCount]int{10, weight, 1, 40})
		}
	}
	if cfg.Exchanges.Bybit.Enabled {
		c.addScope(Bybit, cfg.Upstream.Limits.Bybit, [operationCount]int{})
	}
	for id, s := range c.scopes {
		for _, w := range s.windows {
			if !s.feasible(w) {
				return nil, fmt.Errorf("%s bootstrap allowance cannot admit a permitted request", id)
			}
		}
	}
	return c, nil
}

func percent(value, share int) int { return value/100*share + value%100*share/100 }

func (c *Controller) addScope(id Scope, cfg config.Scope, costs [operationCount]int) {
	shares := c.settings.OperationSharePercent
	s := &scopeState{windows: make(map[string]window), spacing: cfg.MinRequestSpacing, historySince: c.clock.Now(), maxCosts: costs, shares: [operationCount]int{shares.Tickers, shares.Klines, shares.Instruments, shares.MarketStats}}
	if id == Bybit {
		s.shares[Tickers] += s.shares[MarketStats]
		s.shares[MarketStats] = 0
	}
	bootstrap := config.Defaults().Upstream.Limits
	source := bootstrap.BinanceSpot
	if id == BinanceLinear {
		source = bootstrap.BinanceLinear
	}
	if id == Bybit {
		source = bootstrap.Bybit
	}
	for key, w := range cfg.Windows {
		ceiling := source.Windows[key].Limit
		threshold := c.settings.Binance.StopThresholdPercent
		userCap := 0
		if id == Bybit || w.ExplicitLimit || w.Limit != ceiling {
			userCap = w.Limit
		}
		if id == Bybit {
			threshold = 100 - c.settings.SafetyMarginPercent
		}
		effective := ceiling
		if userCap > 0 {
			effective = min(effective, userCap)
		}
		s.windows[key] = window{unit: w.Unit, duration: w.Window, ceiling: ceiling, userCap: userCap, source: "bootstrap", limit: percent(effective, threshold), split: w.SplitOperations, funding: key == "funding_requests_5m"}
	}
	c.scopes[id] = s
}

func laneIndex(scope Scope, op Operation) int {
	if scope == Bybit {
		return int(operationCount + op)
	}
	return int(op)
}

func (c *Controller) notify() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func (w window) cost(c cost) int {
	if w.funding && !c.funding {
		return 0
	}
	if w.unit == "weight" {
		return c.weight
	}
	return 1
}

func (s *scopeState) ready(w *waiter, now time.Time) (time.Time, error) {
	ready := maxTime(s.next, s.cooldown, w.notBefore)
	longest := time.Duration(0)
	for _, limit := range s.windows {
		longest = max(longest, limit.duration)
	}
	cutoff := now.Add(-longest)
	n := 0
	for n < len(s.history) && !s.history[n].at.After(cutoff) {
		n++
	}
	s.history = slices.Delete(s.history, 0, n)
	if cutoff.After(s.historySince) {
		s.historySince = cutoff
	}
	for name, limit := range s.windows {
		amount := limit.cost(w.cost)
		if amount == 0 {
			continue
		}
		allowance := percent(limit.limit, s.shares[w.cost.operation])
		if amount > limit.limit || limit.split && amount > allowance {
			capacity := limit.limit
			if limit.split {
				capacity = min(capacity, allowance)
			}
			return time.Time{}, fmt.Errorf("%s window %s: request cost %d exceeds allowance %d: %w", w.scope, name, amount, capacity, application.ErrUpstreamUnavailable)
		}
		common, own := 0, 0
		first := now.Add(-limit.duration)
		for _, e := range s.history {
			if !e.at.After(first) {
				continue
			}
			used := limit.cost(e.cost)
			common += used
			if e.cost.operation == w.cost.operation {
				own += used
			}
		}
		if common <= limit.limit-amount && (!limit.split || own <= allowance-amount) {
			continue
		}
		for _, e := range s.history {
			if !e.at.After(first) {
				continue
			}
			used := limit.cost(e.cost)
			common -= used
			if e.cost.operation == w.cost.operation {
				own -= used
			}
			if common <= limit.limit-amount && (!limit.split || own <= allowance-amount) {
				ready = maxTime(ready, e.at.Add(limit.duration))
				break
			}
		}
	}
	return ready, nil
}

func maxTime(values ...time.Time) time.Time {
	var result time.Time
	for _, v := range values {
		if v.After(result) {
			result = v
		}
	}
	return result
}

// acquire is private: callers cannot collect permissions for later dispatch.
func (c *Controller) acquire(ctx context.Context, scope Scope, cost cost, operation *operationState, notBefore time.Time) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.scopes[scope]
	if !ok {
		return time.Time{}, application.ErrUnsupportedOperation
	}
	index := laneIndex(scope, cost.operation)
	l := &c.lanes[index]
	if l.capacity <= len(l.queue) || c.queued >= c.settings.MaxAdmissionWaiters {
		return time.Time{}, application.ErrServiceOverloaded
	}
	w := &waiter{ctx: ctx, scope: scope, cost: cost, operation: operation, expires: c.clock.Now().Add(c.settings.AdmissionTimeout), notBefore: notBefore}
	l.queue = append(l.queue, w)
	c.queued++
	c.notify()
	defer func() {
		at := slices.Index(l.queue, w)
		l.queue = slices.Delete(l.queue, at, at+1)
		c.queued--
		c.notify()
	}()
	for {
		now := c.clock.Now()
		if err := ctx.Err(); err != nil {
			return time.Time{}, err
		}
		if operation.attempts >= operation.maximum {
			return time.Time{}, application.ErrUpstreamAttemptLimit
		}
		ready, err := s.ready(w, now)
		if err != nil {
			return time.Time{}, err
		}
		if deadline, ok := ctx.Deadline(); ok && !s.cooldown.Before(deadline) {
			return time.Time{}, application.ErrUpstreamUnavailable
		}
		if !now.Before(w.expires) {
			return time.Time{}, application.ErrServiceOverloaded
		}
		if l.queue[0] == w && !ready.After(now) && c.selected(now) == index {
			// This is the dispatch boundary. No external work occurs before RoundTrip.
			operation.attempts++
			s.history = append(s.history, entry{at: now, cost: cost})
			s.next = now.Add(s.spacing)
			c.active++
			l.active++
			c.nextLane = (index + 1) % len(c.lanes)
			return now, nil
		}
		until := w.expires
		if ready.After(now) && ready.Before(until) {
			until = ready
		}
		changed := c.changed
		c.mu.Unlock()
		err = wait(ctx, c.clock, until, changed)
		c.mu.Lock()
		if err != nil {
			return time.Time{}, err
		}
	}
}

func (c *Controller) selected(now time.Time) int {
	if c.active >= c.settings.MaxHTTPInflight {
		return -1
	}
	for offset := range len(c.lanes) {
		index := (c.nextLane + offset) % len(c.lanes)
		l := &c.lanes[index]
		if len(l.queue) == 0 || l.active >= l.slots {
			continue
		}
		w := l.queue[0]
		if w.ctx.Err() != nil || !now.Before(w.expires) || w.operation.attempts >= w.operation.maximum {
			continue
		}
		ready, err := c.scopes[w.scope].ready(w, now)
		if err == nil && !ready.After(now) {
			return index
		}
	}
	return -1
}

func (c *Controller) release(scope Scope, op Operation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active--
	c.lanes[laneIndex(scope, op)].active--
	c.notify()
}

func (c *Controller) Cooldown(scope Scope, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.scopes[scope]; s != nil {
		s.cooldown = maxTime(s.cooldown, until)
		c.notify()
	}
}
