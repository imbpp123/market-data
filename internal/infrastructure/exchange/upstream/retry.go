package upstream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
)

// Jitter returns a delay in [0, ceiling]. It must be safe for concurrent use.
type Jitter func(ceiling time.Duration) time.Duration

func backoff(settings config.Retry, failures int, jitter Jitter) time.Duration {
	ceiling := min(settings.InitialBackoff, settings.MaxBackoff)
	for i := 1; i < failures && ceiling < settings.MaxBackoff; i++ {
		if ceiling > settings.MaxBackoff/2 {
			ceiling = settings.MaxBackoff
		} else {
			ceiling *= 2
		}
	}
	return max(0, min(ceiling, jitter(ceiling)))
}

// CycleGate is owned by one background worker and survives failed cycles.
// Run receives the full cycle, including normalization and publication.
// Individual successful pages do not reset failure backoff.
type CycleGate struct {
	mu       sync.Mutex
	clock    Clock
	jitter   Jitter
	retry    config.Retry
	interval time.Duration
	failures int
	next     time.Time
	running  bool
	deferred application.RefreshDeferral
}

func NewCycleGate(clock Clock, jitter Jitter, retry config.Retry, interval time.Duration) (*CycleGate, error) {
	if clock == nil || jitter == nil || retry.InitialBackoff <= 0 || retry.MaxBackoff < retry.InitialBackoff || interval < 0 {
		return nil, fmt.Errorf("invalid cycle backoff settings")
	}
	return &CycleGate{clock: clock, jitter: jitter, retry: retry, interval: interval}, nil
}

func (g *CycleGate) wait(ctx context.Context) error {
	g.mu.Lock()
	next, deferred := g.next, g.deferred
	g.mu.Unlock()
	if deferred != nil {
		if err := deferred.Wait(ctx); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		g.mu.Lock()
		g.deferred = nil
		g.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !next.After(g.clock.Now()) {
		return nil
	}
	return wait(ctx, g.clock, next, nil)
}

func (g *CycleGate) complete(success bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delay := g.interval
	if success {
		g.failures = 0
	} else {
		// The capped exponent needs no unbounded failure counter.
		g.failures = min(g.failures+1, 64)
		delay = max(delay, backoff(g.retry, g.failures, g.jitter))
	}
	g.next = g.clock.Now().Add(delay)
}

// Run rejects overlapping cycles and keeps backoff across successive calls.
// The callback starts one bounded controller operation and includes publication.
func (g *CycleGate) Run(ctx context.Context, cycle func(context.Context) error) error {
	g.mu.Lock()
	if g.running {
		g.mu.Unlock()
		return application.ErrServiceOverloaded
	}
	g.running = true
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.running = false
		g.mu.Unlock()
	}()
	if err := g.wait(ctx); err != nil {
		return err
	}
	err := cycle(ctx)
	if deferred := application.DeferredRefresh(err); deferred != nil {
		g.mu.Lock()
		g.deferred = deferred
		g.mu.Unlock()
	} else {
		g.complete(err == nil)
	}
	return err
}
