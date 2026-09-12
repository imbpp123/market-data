package kline

import (
	"context"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"
)

type Series struct {
	application.Scope
	Symbol   string
	Interval domain.Timeframe
}

// Query uses aligned half-open boundaries, selecting candles by OpenTime.
type Query struct {
	Series
	From time.Time
	To   time.Time
}

// Request is one planned page. Limit is the actual slot count to send, bounded
// by the explicit configured page limit; SDK parameters belong to adapters.
type Request struct {
	Query
	Limit int
}

// Stored carries internal fill evidence alongside the public domain candle.
// RequestStartedAt is the successful HTTP attempt's start after admission,
// measured by the adapter for this page, not the logical fill or first retry.
// A candle is final only when RequestStartedAt >= Candle.CloseTime. Receipt
// time alone does not prove finality. Never serialize Stored as a public DTO.
type Stored struct {
	Candle           domain.Kline
	RequestStartedAt time.Time
}

// Repository honors ctx and owns all inputs and returned slices/pointers.
// GetRange returns available rows sorted by OpenTime; gaps remain gaps.
// UpsertMany validates the entire batch (scope, boundaries, duplicate keys and
// RequestStartedAt <= FetchedAt) before atomic merge. Failures preserve state.
// Final rows are immutable; final evidence supersedes intermediate data.
// Intermediate rows order by RequestStartedAt, then FetchedAt; ties keep the
// stored row. An older result must never overwrite a newer open value.
// DeleteBefore removes OpenTime strictly below the scoped cutoff for all
// symbols, records a monotonic cutoff, and prevents late writes below it.
// Merges prune against the applied cutoff. Missing rows are not cached as data.
type Repository interface {
	GetRange(ctx context.Context, query Query) ([]Stored, error)
	UpsertMany(ctx context.Context, rows []Stored) error
	DeleteBefore(ctx context.Context, scope application.Scope, interval domain.Timeframe, before time.Time) error
}

// Provider owns returned data and honors ctx through admission, retries and I/O.
// GetKlines returns one normalized page with attempt metadata, ascending and
// without duplicate OpenTime. Invalid rows fail the page; empty success does
// not prove completeness. Earlier saved pages may survive a later page error.
// SupportedTimeframes is a local lookup returning a safe copy from the same
// adapter table used for conversion. Unsupported markets return ErrInvalidFilter;
// unsupported intervals return ErrInvalidInterval before any I/O.
type Provider interface {
	Exchange() domain.Exchange
	SupportedTimeframes(market domain.Market) ([]domain.Timeframe, error)
	GetKlines(ctx context.Context, request Request) ([]Stored, error)
}
