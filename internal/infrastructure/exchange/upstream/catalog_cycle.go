package upstream

import (
	"context"
	"errors"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
)

// CatalogCycle uses one instrument worker for both schedules. A slow refresh
// delays the next job; it cannot create a second in-flight catalog request.
type CatalogCycle struct {
	controller  *Controller
	scope       Scope
	instruments *CycleGate
	catalog     *CycleGate
	refresh     func(context.Context) error
}

func NewCatalogCycle(controller *Controller, scope Scope, clock Clock, jitter Jitter, retry config.Retry, instrumentInterval, catalogInterval time.Duration, refresh func(context.Context) error) (*CatalogCycle, error) {
	instruments, err := NewCycleGate(clock, jitter, retry, instrumentInterval)
	if err != nil {
		return nil, err
	}
	catalog, err := NewCycleGate(clock, jitter, retry, catalogInterval)
	if err != nil {
		return nil, err
	}
	return &CatalogCycle{controller: controller, scope: scope, instruments: instruments, catalog: catalog, refresh: refresh}, nil
}

func (c *CatalogCycle) Run(ctx context.Context, refreshInstruments func(context.Context) error) error {
	gate, lastCatalog, err := c.next(ctx)
	if err != nil {
		return err
	}
	refresh := refreshInstruments
	if gate == c.catalog {
		refresh = c.refresh
	}

	err = NewBoundedCycle(gate, c.controller, c.scope, Instruments).Run(ctx, refresh)
	if gate == c.instruments {
		c.controller.mu.Lock()
		updatedAt := c.controller.scopes[c.scope].catalogUpdatedAt
		c.controller.mu.Unlock()
		if updatedAt.After(lastCatalog) {
			c.catalog.failures = 0
			c.catalog.next = updatedAt.Add(c.catalog.interval)
		} else if application.DeferredRefresh(err) == nil {
			c.catalog.complete(err == nil)
		}
	}
	return err
}

// next keeps the two schedules independent when only one request family is
// blocked. Both jobs still run on the same worker and cannot overlap.
func (c *CatalogCycle) next(ctx context.Context) (*CycleGate, time.Time, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, time.Time{}, err
		}
		c.controller.mu.Lock()
		now := c.controller.clock.Now()
		lastCatalog := c.controller.scopes[c.scope].catalogUpdatedAt
		instrumentDue := c.due(c.instruments, now)
		catalogDue := c.due(c.catalog, now)
		if !catalogDue.IsZero() && !lastCatalog.IsZero() {
			catalogDue = maxTime(catalogDue, lastCatalog.Add(c.catalog.interval))
		}
		changed := c.controller.changed
		c.controller.mu.Unlock()

		gate, due := c.instruments, instrumentDue
		if due.IsZero() || !catalogDue.IsZero() && catalogDue.Before(due) {
			gate, due = c.catalog, catalogDue
		}
		if !due.IsZero() && !due.After(now) {
			return gate, lastCatalog, nil
		}
		if err := wait(ctx, c.controller.clock, due, changed); err != nil {
			return nil, time.Time{}, err
		}
	}
}

// due is called under the controller lock by the single owning worker.
func (c *CatalogCycle) due(gate *CycleGate, now time.Time) time.Time {
	due := maxTime(now, gate.next)
	var deferred *budgetDeferral
	if errors.As(gate.deferred, &deferred) {
		next, err := deferred.ready(now)
		if err != nil || !next.IsZero() && !next.After(now) {
			// An impossible cost returns through the ordinary refresh error path.
			gate.deferred = nil
		} else if next.IsZero() {
			return time.Time{}
		} else {
			due = maxTime(due, next)
		}
	}
	return due
}
