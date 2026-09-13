package upstream

import (
	"context"
	"time"

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
	c.controller.mu.Lock()
	lastCatalog := c.controller.scopes[c.scope].catalogUpdatedAt
	c.controller.mu.Unlock()

	catalogDue := c.catalog.next
	if !lastCatalog.IsZero() {
		catalogDue = maxTime(catalogDue, lastCatalog.Add(c.catalog.interval))
	}
	gate, refresh := c.instruments, refreshInstruments
	if !c.instruments.next.IsZero() && catalogDue.Before(c.instruments.next) {
		gate, refresh = c.catalog, c.refresh
		gate.next = catalogDue
	}

	err := NewBoundedCycle(gate, c.controller, c.scope, Instruments).Run(ctx, refresh)
	if gate == c.instruments {
		c.controller.mu.Lock()
		updatedAt := c.controller.scopes[c.scope].catalogUpdatedAt
		c.controller.mu.Unlock()
		if updatedAt.After(lastCatalog) {
			c.catalog.failures = 0
			c.catalog.next = updatedAt.Add(c.catalog.interval)
		} else {
			c.catalog.complete(err == nil)
		}
	}
	return err
}
