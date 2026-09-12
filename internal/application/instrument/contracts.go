package instrument

import (
	"context"

	"market-data/internal/application"
	"market-data/internal/domain"
)

type Filter struct {
	application.SnapshotFilter
	Status *domain.InstrumentStatus
}

// Repository operations honor cancellation and return owned values, including
// optional pointers. ReplaceSnapshot copies inputs, rejects duplicate symbols
// and wrong-scope rows, and publishes atomically. Errors preserve prior state.
// Successful empty replacement marks this scope ready and removes prior rows.
// List checks readiness of every selected scope before filtering; an unready
// scope returns application.ErrDataNotReady. Rows sort by exchange/market/symbol.
type Repository interface {
	ReplaceSnapshot(ctx context.Context, scope application.Scope, rows []domain.Instrument) error
	List(ctx context.Context, filter Filter) ([]domain.Instrument, error)
	HasSnapshot(ctx context.Context, scope application.Scope) (bool, error)
}

// Provider returns a complete owned catalog or an error, never a partial
// successful catalog for its fixed scope. All blocking work must honor ctx.
type Provider interface {
	Scope() application.Scope
	GetInstruments(ctx context.Context) ([]domain.Instrument, error)
}
