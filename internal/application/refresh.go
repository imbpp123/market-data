package application

import (
	"context"
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
