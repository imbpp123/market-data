package application

import (
	"context"
	"errors"
	"time"
)

// Window is zero for tickers and 24h for market statistics.
type RefreshEvent struct {
	Scope     Scope
	Window    time.Duration
	FetchedAt time.Time
	Size      int
	Error     error
}

type CycleRunner interface {
	Run(context.Context, func(context.Context) error) error
}

func RunRefresh(ctx context.Context, cycles CycleRunner, refresh func(context.Context) error) error {
	for ctx.Err() == nil {
		if err := cycles.Run(ctx, refresh); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}

	return ctx.Err()
}

// RefreshDeferral is a temporary upstream shortage, not a failed refresh.
// A zero next time means completion or another state change is required.
type RefreshDeferral interface {
	error
	Reason() string
	NextEligible() time.Time
	Wait(context.Context) error
}

func DeferredRefresh(err error) RefreshDeferral {
	var deferred RefreshDeferral
	if errors.As(err, &deferred) {
		return deferred
	}
	return nil
}
