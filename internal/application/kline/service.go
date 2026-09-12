package kline

import (
	"context"
	"slices"
	"sync"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"

	"golang.org/x/sync/singleflight"
)

// Operations owns a whole fill's admission deadline and attempt budget.
// Call onAttempt immediately before each actual dispatch, including retries.
// All pages must share the context passed to run. Callbacks finish before Run.
type Operations interface {
	Run(ctx context.Context, scope application.Scope, onAttempt func(), run func(context.Context) error) error
}

type ScopeSettings struct {
	Scope     application.Scope
	Provider  Provider
	PageLimit int
}

type Settings struct {
	HistoryCandles            int64
	MaxCallers                int
	MaxActiveFills            int
	MaxActiveFillsPerExchange int
	MaxAttempts               int
	FillTimeout               time.Duration
}

type Event struct {
	Scope       application.Scope
	CacheRead   bool
	CacheHit    bool
	FillStarted bool
	SharedWait  bool
	Attempts    int
	Downloaded  int
	Completed   bool
	Duration    time.Duration
	Symbol      string
	Interval    domain.Timeframe
	Error       error
}

type serviceScope struct {
	planner  *Planner
	provider Provider
}

type Service struct {
	root        context.Context
	repository  Repository
	instruments instrument.Repository
	operations  Operations
	now         func() time.Time
	observe     func(Event)
	settings    Settings
	scopes      map[application.Scope]serviceScope
	callers     chan struct{}
	mu          sync.Mutex
	stopped     bool
	fills       map[Series]*fill
	active      map[domain.Exchange]int
	group       singleflight.Group
	owned       sync.WaitGroup
}

func NewService(root context.Context, repository Repository, instruments instrument.Repository, operations Operations, scopes []ScopeSettings, settings Settings, now func() time.Time, observe func(Event)) (*Service, error) {
	if root == nil || repository == nil || instruments == nil || operations == nil || now == nil || len(scopes) == 0 || settings.MaxCallers <= 0 || settings.MaxActiveFills <= 0 || settings.MaxActiveFillsPerExchange <= 0 || settings.MaxAttempts <= 0 || settings.FillTimeout <= 0 {
		return nil, application.ErrInvalidParameter
	}

	s := &Service{root: root, repository: repository, instruments: instruments, operations: operations, now: now, observe: observe, settings: settings,
		scopes: make(map[application.Scope]serviceScope), callers: make(chan struct{}, settings.MaxCallers), fills: make(map[Series]*fill), active: make(map[domain.Exchange]int)}
	for _, scope := range scopes {
		if scope.Provider == nil || scope.Provider.Exchange() != scope.Scope.Exchange {
			return nil, application.ErrInvalidParameter
		}
		if _, exists := s.scopes[scope.Scope]; exists {
			return nil, application.ErrInvalidParameter
		}
		supported, err := scope.Provider.SupportedTimeframes(scope.Scope.Market)
		if err != nil {
			return nil, err
		}
		planner, err := NewPlanner(scope.Scope, supported, settings.HistoryCandles, scope.PageLimit)
		if err != nil {
			return nil, err
		}
		s.scopes[scope.Scope] = serviceScope{planner: planner, provider: scope.Provider}
	}
	return s, nil
}

// Wait closes fill admission and waits for owned work. The owner cancels the
// service lifecycle context first so that active work can stop immediately.
func (s *Service) Wait() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.owned.Wait()
}

func (s *Service) ValidateSeries(series Series) error {
	scope, ok := s.scopes[series.Scope]
	if !ok || series.Symbol == "" {
		return application.ErrInvalidFilter
	}
	_, err := application.SelectSnapshot([]application.Scope{series.Scope}, application.SnapshotQuery{
		Exchange: string(series.Exchange), Market: string(series.Market), Symbol: series.Symbol,
	})
	if err != nil {
		return err
	}
	if !slices.Contains(scope.planner.supported, series.Interval) {
		return application.ErrInvalidInterval
	}
	return nil
}

// Validate checks request bounds without reading storage or taking capacity.
func (s *Service) Validate(query Query) error {
	if err := s.ValidateSeries(query.Series); err != nil {
		return err
	}
	return s.scopes[query.Scope].planner.Validate(query, s.now())
}

func (s *Service) Get(ctx context.Context, query Query) ([]domain.Kline, error) {
	if err := s.Validate(query); err != nil {
		return nil, err
	}
	scope := s.scopes[query.Scope]
	if err := s.contextError(ctx); err != nil {
		return nil, err
	}
	select {
	case s.callers <- struct{}{}:
		defer func() { <-s.callers }()
	default:
		return nil, application.ErrServiceOverloaded
	}

	catalog, err := s.instruments.List(ctx, instrument.Filter{SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{query.Scope}, Symbol: &query.Symbol}})
	if err != nil {
		return nil, err
	}
	if len(catalog) == 0 {
		return nil, application.ErrSymbolNotFound
	}

	refreshed := make(refreshes)
	budget := &callerBudget{remaining: s.settings.MaxAttempts, exhausted: make(chan struct{})}
	var fillError error
	first := true
	for {
		rows, plan, err := s.read(ctx, scope.planner, query, refreshed)
		if err != nil {
			return nil, err
		}
		if first {
			s.emit(Event{Scope: query.Scope, CacheRead: true, CacheHit: len(plan) == 0})
			first = false
		}
		if len(plan) == 0 {
			result := make([]domain.Kline, 0, len(rows))
			for _, row := range rows {
				result = append(result, row.Candle)
			}
			return result, nil
		}
		if fillError != nil {
			return nil, fillError
		}
		if budget.remaining == 0 {
			return nil, application.ErrUpstreamAttemptLimit
		}

		f, err := s.join(ctx, query, refreshed, budget)
		if err != nil {
			return nil, err
		}
		fillError = s.wait(ctx, f, budget)
		select {
		case <-budget.exhausted:
			return nil, application.ErrUpstreamAttemptLimit
		default:
		}
		// A completed fill's evidence is immutable. Failed fills still carry
		// earlier saved pages, which can satisfy a smaller caller's range.
		select {
		case <-f.done:
			refreshed.merge(query, f.saved)
		default:
		}
		if err := s.contextError(ctx); err != nil {
			return nil, err
		}
	}
}

func (s *Service) read(ctx context.Context, planner *Planner, query Query, refreshed refreshes) ([]Stored, []Request, error) {
	if err := s.contextError(ctx); err != nil {
		return nil, nil, err
	}
	now := s.now()
	if err := planner.Validate(query, now); err != nil {
		return nil, nil, err
	}
	rows, err := s.repository.GetRange(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	if err := s.contextError(ctx); err != nil {
		return nil, nil, err
	}
	plan, err := planner.plan(query, rows, now, refreshed)
	return rows, plan, err
}

func (s *Service) contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.root.Err()
}

func (s *Service) emit(event Event) {
	if s.observe != nil {
		s.observe(event)
	}
}

type evidence struct {
	started time.Time
	fetched time.Time
}

type refreshes map[time.Time]evidence

func (r refreshes) accepts(row Stored, now time.Time) bool {
	e, ok := r[row.Candle.OpenTime.UTC()]
	return ok && now.Before(row.Candle.CloseTime) && !row.RequestStartedAt.Before(e.started) &&
		(!row.RequestStartedAt.Equal(e.started) || !row.Candle.FetchedAt.Before(e.fetched))
}

func (r refreshes) merge(query Query, other refreshes) {
	for open, e := range other {
		if !open.Before(query.From) && open.Before(query.To) {
			previous, ok := r[open]
			if !ok || e.started.After(previous.started) || (e.started.Equal(previous.started) && e.fetched.After(previous.fetched)) {
				r[open] = e
			}
		}
	}
}
