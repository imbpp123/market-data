package instrument

import (
	"context"
	"fmt"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"

	"market-data/internal/application"
	"market-data/internal/domain"
)

type Query struct {
	Exchange string
	Market   string
	Symbol   string
	Status   string
}

type Reader struct {
	repository Repository
	scopes     []application.Scope
}

func NewReader(repository Repository, scopes []application.Scope) *Reader {
	return &Reader{repository: repository, scopes: slices.Clone(scopes)}
}

// Validate checks filters without reading the repository or taking a caller slot.
func (s *Reader) Validate(query Query) error {
	_, err := s.filter(query)
	return err
}

func (s *Reader) List(ctx context.Context, query Query) ([]domain.Instrument, error) {
	filter, err := s.filter(query)
	if err != nil {
		return nil, err
	}

	return s.repository.List(ctx, filter)
}

func (s *Reader) filter(query Query) (Filter, error) {
	filter := Filter{}
	if query.Exchange != "" && !domain.Exchange(query.Exchange).Valid() {
		return filter, application.ErrInvalidFilter
	}

	if query.Market != "" && !domain.Market(query.Market).Valid() {
		return filter, application.ErrInvalidFilter
	}

	for _, scope := range s.scopes {
		if (query.Exchange == "" || query.Exchange == string(scope.Exchange)) && (query.Market == "" || query.Market == string(scope.Market)) {
			filter.Scopes = append(filter.Scopes, scope)
		}
	}

	if len(filter.Scopes) == 0 {
		return filter, application.ErrInvalidFilter
	}

	if query.Symbol != "" {
		if len(query.Symbol) > 128 || !utf8.ValidString(query.Symbol) {
			return filter, application.ErrInvalidFilter
		}

		for _, r := range query.Symbol {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return filter, application.ErrInvalidFilter
			}
		}

		filter.Symbol = &query.Symbol
	}

	if query.Status != "" {
		status := domain.InstrumentStatus(query.Status)
		if !status.Valid() {
			return filter, application.ErrInvalidStatus
		}

		filter.Status = &status
	}

	return filter, nil
}

// RefreshEvent reports the outcome at the atomic publication boundary.
type RefreshEvent struct {
	Scope     application.Scope
	UpdatedAt time.Time
	Size      int
	Error     error
}

type Refresher struct {
	provider   Provider
	repository Repository
	now        func() time.Time
	observe    func(RefreshEvent)
}

func NewRefresher(provider Provider, repository Repository, now func() time.Time, observe func(RefreshEvent)) *Refresher {
	return &Refresher{provider: provider, repository: repository, now: now, observe: observe}
}

func (s *Refresher) Refresh(ctx context.Context) (err error) {
	event := RefreshEvent{Scope: s.provider.Scope()}
	defer func() {
		event.Error = err
		if s.observe != nil {
			s.observe(event)
		}
	}()

	rows, err := s.provider.GetInstruments(ctx)
	if err != nil {
		return fmt.Errorf("fetch instruments: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	updatedAt := s.now().UTC()
	for i := range rows {
		rows[i].UpdatedAt = updatedAt
	}

	if err := s.repository.ReplaceSnapshot(ctx, event.Scope, rows); err != nil {
		return fmt.Errorf("publish instruments: %w", err)
	}

	event.UpdatedAt = updatedAt
	event.Size = len(rows)
	return nil
}

// CycleRunner owns scheduling, operation bounds, and failure backoff.
type CycleRunner interface {
	Run(context.Context, func(context.Context) error) error
}

func (s *Refresher) Run(ctx context.Context, cycles CycleRunner) error {
	for ctx.Err() == nil {
		// Refresh reports failures. One unavailable scope must not stop other workers.
		if err := cycles.Run(ctx, s.Refresh); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}

	return ctx.Err()
}
