package upstream

import "context"

type attemptObserverKey struct{}

// WithAttemptObserver records dispatches for the owning application operation.
// The callback must be concurrency-safe and must not block on upstream work.
func WithAttemptObserver(ctx context.Context, observe func()) context.Context {
	return context.WithValue(ctx, attemptObserverKey{}, observe)
}

func observeAttempt(ctx context.Context) {
	if observe, ok := ctx.Value(attemptObserverKey{}).(func()); ok && observe != nil {
		observe()
	}
}
