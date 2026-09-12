// Package application contains shared use-case contracts, without transport
// status codes or concrete infrastructure dependencies.
package application

import (
	"time"

	"market-data/internal/domain"
)

type Scope struct {
	Exchange domain.Exchange
	Market   domain.Market
}

// SnapshotFilter carries explicit, validated scopes selected from enabled
// configuration. An empty scope list selects nothing, never all stored data.
// A nil Symbol means no symbol filter. Repositories do not discover scopes.
type SnapshotFilter struct {
	Scopes []Scope
	Symbol *string
}

// ExchangeCapabilities describes collection behavior, not configured page limits.
// Implementations return owned slices. v1 supports only the rolling 24h window.
type ExchangeCapabilities struct {
	MarketStatsWithTicker bool
	MarketStatsWindows    []time.Duration
}
