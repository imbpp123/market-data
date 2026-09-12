package upstream

import (
	"context"
	"time"
)

// Clock owns both timestamps and cancelable timers.
type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

type Timer interface {
	C() <-chan time.Time
	Stop()
}

type SystemClock struct{}

func (SystemClock) Now() time.Time                 { return time.Now() }
func (SystemClock) NewTimer(d time.Duration) Timer { return systemTimer{time.NewTimer(d)} }

type systemTimer struct{ timer *time.Timer }

func (t systemTimer) C() <-chan time.Time { return t.timer.C }
func (t systemTimer) Stop()               { t.timer.Stop() }

func wait(ctx context.Context, clock Clock, until time.Time, changed <-chan struct{}) error {
	var timer Timer
	var tick <-chan time.Time
	if !until.IsZero() {
		timer = clock.NewTimer(max(0, until.Sub(clock.Now())))
		tick = timer.C()
		defer timer.Stop()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-changed:
		return nil
	case <-tick:
		return nil
	}
}
