package marketstats

import (
	"context"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"
)

type Filter struct {
	application.SnapshotFilter
	Window time.Duration
}

// Repository honors ctx and copies all inputs and outputs, including pointers.
// ReplaceSnapshot rejects duplicate and wrong-scope/window rows, then atomically
// replaces only this scope/window. Errors preserve prior state; empty success
// is ready. Readiness is independent of instruments, tickers, and other windows.
// List requires all selected scopes ready before symbol filtering, otherwise
// returns application.ErrDataNotReady; rows sort by exchange/market/symbol.
// Get returns ErrDataNotReady for an unready scope/window and found=false for
// an absent symbol in a ready snapshot. Reads keep original timestamps.
type Repository interface {
	ReplaceSnapshot(ctx context.Context, scope application.Scope, window time.Duration, rows []domain.MarketStats) error
	Get(ctx context.Context, scope application.Scope, symbol string, window time.Duration) (row domain.MarketStats, found bool, err error)
	List(ctx context.Context, filter Filter) ([]domain.MarketStats, error)
	HasSnapshot(ctx context.Context, scope application.Scope, window time.Duration) (bool, error)
}

// Provider returns complete owned snapshots and honors ctx. Unsupported windows
// fail with ErrUnsupportedWindow before I/O. Providers with shared statistics
// return ErrUnsupportedOperation from GetMarketStats without I/O.
type Provider interface {
	Exchange() domain.Exchange
	Capabilities() application.ExchangeCapabilities
	GetMarketStats(ctx context.Context, market domain.Market, window time.Duration) ([]domain.MarketStats, error)
}
