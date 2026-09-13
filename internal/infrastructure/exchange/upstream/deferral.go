package upstream

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"market-data/internal/application"
)

type budgetDeferral struct {
	controller *Controller
	scope      Scope
	cost       cost
	reason     string
	next       time.Time
}

func (d *budgetDeferral) Error() string {
	return fmt.Sprintf("%s operation %d budget: %s: %v", d.scope, d.cost.operation, d.reason, application.ErrServiceOverloaded)
}

func (d *budgetDeferral) Unwrap() error { return application.ErrServiceOverloaded }

func (d *budgetDeferral) Reason() string { return d.reason }

func (d *budgetDeferral) NextEligible() time.Time { return d.next }

func (d *budgetDeferral) Wait(ctx context.Context) error {
	c := d.controller
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		now := c.clock.Now()
		next, err := d.ready(now)
		changed := c.changed
		c.mu.Unlock()
		if err != nil {
			return err
		}
		if !next.IsZero() && !next.After(now) {
			return nil
		}
		if err := wait(ctx, c.clock, next, changed); err != nil {
			return err
		}
	}
}

// ready runs with the controller lock and never reserves or spends an attempt.
func (d *budgetDeferral) ready(now time.Time) (time.Time, error) {
	next, err := d.controller.ready(&waiter{scope: d.scope, cost: d.cost}, now)
	var blocked *budgetDeferral
	if errors.As(err, &blocked) {
		return blocked.next, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return maxTime(now, next), nil
}

// budgetReady predicts recovery from known expiries. In-flight costs stay live
// in this prediction; completion will notify waiters. Search costs O(n log n).
func (s *scopeState) budgetReady(w window, request cost, now time.Time) time.Time {
	var expiries []time.Time
	for _, e := range s.history {
		if !e.inflight && w.cost(e.cost) > 0 && e.at.Add(w.duration).After(now) {
			expiries = append(expiries, e.at.Add(w.duration))
		}
		if w.unit == "weight" {
			if observation, ok := e.observations[w.duration]; ok && observation.expires.After(now) {
				expiries = append(expiries, observation.expires)
			}
		}
	}
	slices.SortFunc(expiries, func(a, b time.Time) int { return a.Compare(b) })
	allowance := percent(w.limit, s.shares[request.operation])
	index, end := 0, len(expiries)
	for index < end {
		middle := index + (end-index)/2
		usage := s.usage(w, expiries[middle])
		if usage.common <= w.limit && (!w.split || usage.operations[request.operation] <= allowance-w.cost(request)) {
			end = middle
		} else {
			index = middle + 1
		}
	}
	if index == len(expiries) {
		return time.Time{}
	}
	return expiries[index]
}
