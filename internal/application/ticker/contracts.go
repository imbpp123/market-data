package ticker

import (
	"context"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"
)

// Repository honors ctx and copies inputs and outputs, including pointers.
// Replacement rejects wrong-scope and duplicate rows before atomic publication;
// errors preserve prior data. An empty successful snapshot is ready.
// List requires every selected scope ready before symbol filtering, otherwise
// returns application.ErrDataNotReady. Rows sort by exchange/market/symbol.
// Get returns ErrDataNotReady for an unready scope and found=false for an absent
// symbol in a ready scope. Reads never refresh data or change timestamps.
type Repository interface {
	ReplaceSnapshot(ctx context.Context, scope application.Scope, rows []domain.Ticker) error
	Get(ctx context.Context, scope application.Scope, symbol string) (row domain.Ticker, found bool, err error)
	List(ctx context.Context, filter application.SnapshotFilter) ([]domain.Ticker, error)
	HasSnapshot(ctx context.Context, scope application.Scope) (bool, error)
}

// Collection holds independent complete branch outcomes. A nil branch error
// with an empty slice is success. HasMarketStats distinguishes no stats branch
// from a successfully empty one. Publication errors also stay branch-local.
type Collection struct {
	FetchedAt   time.Time
	Tickers     []domain.Ticker
	TickerError error

	HasMarketStats   bool
	MarketStats      []domain.MarketStats
	MarketStatsError error
}

// Provider returns owned values and honors ctx for all blocking work. An outer
// GetTickers error invalidates both branches. A successful shared response may
// fail normalization in one branch without invalidating the other.
// Capabilities is a local lookup returning a safe copy; workers use the shared
// statistics flag instead of exchange-name checks.
type Provider interface {
	Exchange() domain.Exchange
	Capabilities() application.ExchangeCapabilities
	GetTickers(ctx context.Context, market domain.Market) (Collection, error)
}
