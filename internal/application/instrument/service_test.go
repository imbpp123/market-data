package instrument_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReaderFiltersEnabledScopes(t *testing.T) {
	cases := []struct {
		name    string
		query   instrument.Query
		symbols []string
		want    error
	}{
		{"all sorted", instrument.Query{}, []string{"A", "B", "C"}, nil},
		{"exchange", instrument.Query{Exchange: "bybit"}, []string{"C"}, nil},
		{"market", instrument.Query{Market: "spot"}, []string{"B", "C"}, nil},
		{"symbol", instrument.Query{Symbol: "B"}, []string{"B"}, nil},
		{"status", instrument.Query{Status: "halted"}, []string{"B"}, nil},
		{"absent", instrument.Query{Symbol: "ABSENT"}, []string{}, nil},
		{"exact symbol", instrument.Query{Symbol: "b"}, []string{}, nil},
		{"unknown exchange", instrument.Query{Exchange: "other"}, nil, application.ErrInvalidFilter},
		{"uppercase exchange", instrument.Query{Exchange: "BINANCE"}, nil, application.ErrInvalidFilter},
		{"unknown market", instrument.Query{Market: "inverse"}, nil, application.ErrInvalidFilter},
		{"disabled pair", instrument.Query{Exchange: "bybit", Market: "linear"}, nil, application.ErrInvalidFilter},
		{"bad status", instrument.Query{Status: "Trading"}, nil, application.ErrInvalidStatus},
		{"symbol whitespace", instrument.Query{Symbol: "A B"}, nil, application.ErrInvalidFilter},
		{"symbol control", instrument.Query{Symbol: "A\x00"}, nil, application.ErrInvalidFilter},
		{"symbol invalid UTF8", instrument.Query{Symbol: "\xff"}, nil, application.ErrInvalidFilter},
		{"symbol too long", instrument.Query{Symbol: strings.Repeat("x", 129)}, nil, application.ErrInvalidFilter},
		{"symbol max length", instrument.Query{Symbol: strings.Repeat("x", 128)}, []string{}, nil},
		{"UTF8 symbol", instrument.Query{Symbol: "字"}, []string{}, nil},
		{"filter before status", instrument.Query{Exchange: "bad", Status: "bad"}, nil, application.ErrInvalidFilter},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			repo := memory.NewInstrumentRepository()
			scopes := []application.Scope{{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}, {Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}, {Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}}
			for i, scope := range scopes {
				symbol := []string{"B", "A", "C"}[i]
				status := domain.InstrumentStatusTrading
				if symbol == "B" {
					status = domain.InstrumentStatusHalted
				}
				require.NoError(t, repo.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: symbol, Status: status}}))
			}
			reader := instrument.NewReader(repo, scopes)
			scopes[0] = application.Scope{}

			rows, err := reader.List(t.Context(), tt.query)

			if tt.want != nil {
				assert.ErrorIs(t, err, tt.want)
				return
			}
			require.NoError(t, err)
			symbols := make([]string, 0, len(rows))
			for _, row := range rows {
				symbols = append(symbols, row.Symbol)
			}
			assert.Equal(t, tt.symbols, symbols)
		})
	}
}

func TestReaderCannotFilterAwayUnreadyScope(t *testing.T) {
	repo := memory.NewInstrumentRepository()
	ready := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	unready := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
	require.NoError(t, repo.ReplaceSnapshot(t.Context(), ready, nil))
	reader := instrument.NewReader(repo, []application.Scope{ready, unready})

	rows, err := reader.List(t.Context(), instrument.Query{Symbol: "ABSENT", Status: "closed"})

	assert.ErrorIs(t, err, application.ErrDataNotReady)
	assert.Nil(t, rows)
}

type provider struct {
	mu   sync.Mutex
	rows []domain.Instrument
	err  error
}

func (*provider) Scope() application.Scope {
	return application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
}

func (p *provider) GetInstruments(ctx context.Context) ([]domain.Instrument, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return slices.Clone(p.rows), p.err
}

type failingRepository struct {
	instrument.Repository
	err error
}

func (r failingRepository) ReplaceSnapshot(context.Context, application.Scope, []domain.Instrument) error {
	return r.err
}

func TestRefreshPreservesSnapshotOnFailure(t *testing.T) {
	cases := []struct {
		name                   string
		fetchError, writeError error
		canceled               bool
	}{
		{"fetch failure", application.ErrUpstream, nil, false},
		{"normalization failure", application.ErrInvalidUpstreamData, nil, false},
		{"repository failure", nil, errors.New("storage failure"), false},
		{"cancellation", nil, nil, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			repo := memory.NewInstrumentRepository()
			scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
			old := []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "OLD", PriceTick: decimal.NewFromInt(1), QtyStep: decimal.NewFromInt(1), UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}
			require.NoError(t, repo.ReplaceSnapshot(t.Context(), scope, old))
			source := &provider{rows: []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "NEW"}}, err: tt.fetchError}
			target := repo
			if tt.writeError != nil {
				target = failingRepository{Repository: repo, err: tt.writeError}
			}
			var events []instrument.RefreshEvent
			refresh := instrument.NewRefresher(source, target, time.Now, func(e instrument.RefreshEvent) { events = append(events, e) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}

			err := refresh.Refresh(ctx)

			require.Error(t, err)
			want := tt.fetchError
			if tt.writeError != nil {
				want = tt.writeError
			}
			if tt.canceled {
				want = context.Canceled
			}
			assert.ErrorIs(t, err, want)
			rows, err := repo.List(t.Context(), instrument.Filter{SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{scope}}})
			require.NoError(t, err)
			assert.Equal(t, old, rows)
			require.Len(t, events, 1)
			assert.ErrorIs(t, events[0].Error, want)
			assert.Zero(t, events[0].UpdatedAt)
		})
	}
}

func TestRefreshReplacesCompleteSnapshotWithOneUTCTime(t *testing.T) {
	cases := []struct {
		name    string
		symbols []string
	}{{"replacement", []string{"A", "B"}}, {"empty catalog", nil}}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			repo := memory.NewInstrumentRepository()
			scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
			require.NoError(t, repo.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "OLD"}}))
			source := &provider{}
			for _, symbol := range tt.symbols {
				source.rows = append(source.rows, domain.Instrument{Exchange: scope.Exchange, Market: scope.Market, Symbol: symbol})
			}
			now := time.Date(2026, 9, 12, 12, 0, 0, 123, time.FixedZone("offset", 2*60*60))
			var event instrument.RefreshEvent
			refresh := instrument.NewRefresher(source, repo, func() time.Time { return now }, func(e instrument.RefreshEvent) { event = e })

			require.NoError(t, refresh.Refresh(t.Context()))

			rows, err := repo.List(t.Context(), instrument.Filter{SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{scope}}})
			require.NoError(t, err)
			require.Len(t, rows, len(tt.symbols))
			for i, row := range rows {
				assert.Equal(t, tt.symbols[i], row.Symbol)
				assert.Equal(t, now.UTC(), row.UpdatedAt)
			}
			assert.NoError(t, event.Error)
			assert.Equal(t, now.UTC(), event.UpdatedAt)
			assert.Equal(t, len(tt.symbols), event.Size)
		})
	}
}

func TestWorkerRefreshesImmediatelyThenWaitsAfterCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		clock := upstream.SystemClock{}
		controller, err := upstream.New(cfg, clock)
		require.NoError(t, err)
		gate, err := upstream.NewCycleGate(clock, func(d time.Duration) time.Duration { return d }, cfg.HTTPClient.Retry, time.Minute)
		require.NoError(t, err)
		cycles := upstream.NewBoundedCycle(gate, controller, upstream.BinanceSpot, upstream.Instruments)
		source := &provider{err: application.ErrUpstream}
		repo := memory.NewInstrumentRepository()
		events := make(chan instrument.RefreshEvent, 4)
		refresh := instrument.NewRefresher(source, repo, time.Now, func(e instrument.RefreshEvent) { events <- e })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- refresh.Run(ctx, cycles) }()
		synctest.Wait()
		require.Len(t, events, 1)
		assert.ErrorIs(t, (<-events).Error, application.ErrUpstream)

		time.Sleep(59 * time.Second)
		synctest.Wait()
		require.Empty(t, events)
		source.mu.Lock()
		source.err = nil
		source.rows = []domain.Instrument{{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot, Symbol: "A"}}
		source.mu.Unlock()
		time.Sleep(time.Second)
		synctest.Wait()

		require.Len(t, events, 1)
		event := <-events
		assert.NoError(t, event.Error)
		assert.Equal(t, 1, event.Size)
		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
	})
}
