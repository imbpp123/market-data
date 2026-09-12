package upstream

import "context"

// BoundedCycle keeps scheduling outside the single operation deadline. All
// pages, retries, normalization, and publication share that operation context.
type BoundedCycle struct {
	gate       *CycleGate
	controller *Controller
	scope      Scope
	kind       Operation
}

func NewBoundedCycle(gate *CycleGate, controller *Controller, scope Scope, kind Operation) *BoundedCycle {
	return &BoundedCycle{gate: gate, controller: controller, scope: scope, kind: kind}
}

func (c *BoundedCycle) Run(ctx context.Context, cycle func(context.Context) error) error {
	return c.gate.Run(ctx, func(ctx context.Context) error {
		operation, cancel, err := c.controller.Begin(ctx, c.scope, c.kind)
		if err != nil {
			return err
		}

		defer cancel()

		return cycle(operation)
	})
}
