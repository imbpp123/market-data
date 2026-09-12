package upstream

import (
	"context"
	"fmt"

	"market-data/internal/application"
)

type operationKey struct{}
type operationState struct {
	owner    *Controller
	scope    Scope
	kind     Operation
	maximum  int
	attempts int // Guarded by the controller mutex, shared by pages and retries.
}

// Begin must be called once per full cycle or fill, not once per page.
// The returned context carries a single deadline and total attempt allowance.
func (c *Controller) Begin(ctx context.Context, scope Scope, kind Operation) (context.Context, context.CancelFunc, error) {
	if kind < 0 || kind >= operationCount || scope == Bybit && kind == MarketStats {
		return nil, nil, application.ErrUnsupportedOperation
	}
	c.mu.Lock()
	_, ok := c.scopes[scope]
	c.mu.Unlock()
	if !ok {
		return nil, nil, application.ErrUnsupportedOperation
	}
	ctx, cancel := context.WithTimeout(ctx, c.cycleTimeout[kind])
	state := &operationState{owner: c, scope: scope, kind: kind, maximum: c.attempts[kind]}
	return context.WithValue(ctx, operationKey{}, state), cancel, nil
}

func (c *Controller) operation(ctx context.Context, scope Scope, kind Operation) (*operationState, error) {
	state, _ := ctx.Value(operationKey{}).(*operationState)
	if state == nil || state.owner != c || state.scope != scope || state.kind != kind {
		return nil, fmt.Errorf("request needs a matching bounded operation: %w", application.ErrUnsupportedOperation)
	}
	return state, nil
}

// Attempts is the total number of dispatched attempts, including failed ones.
func (c *Controller) Attempts(ctx context.Context) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, _ := ctx.Value(operationKey{}).(*operationState)
	if state == nil || state.owner != c {
		return 0
	}
	return state.attempts
}
