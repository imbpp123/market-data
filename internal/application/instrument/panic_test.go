package instrument_test

import (
	"context"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/observability"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type panicProvider struct{ instrument.Provider }

func (p panicProvider) GetInstruments(context.Context) ([]domain.Instrument, error) {
	panic("source panic")
}

type panicRepository struct{ instrument.Repository }

func (r panicRepository) ReplaceSnapshot(context.Context, application.Scope, []domain.Instrument) error {
	panic("storage panic")
}

func TestRefreshPanicPreservesPublicationState(t *testing.T) {
	cases := []struct {
		name               string
		seeded, writePanic bool
		panicValue         string
	}{
		{name: "first fetch", panicValue: "source panic"},
		{name: "first write", writePanic: true, panicValue: "storage panic"},
		{name: "fetch after publication", seeded: true, panicValue: "source panic"},
		{name: "write after publication", seeded: true, writePanic: true, panicValue: "storage panic"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			repo := memory.NewInstrumentRepository()
			source := &provider{}
			scope := source.Scope()
			source.rows = []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "BTCUSDT"}}
			at := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
			metrics := observability.NewInstruments()
			if tt.seeded {
				seed := instrument.NewRefresher(source, repo, func() time.Time { return at }, metrics.Observe)
				require.NoError(t, seed.Refresh(t.Context()))
			}
			before := metrics.Snapshot()[scope]
			var upstream instrument.Provider = panicProvider{Provider: source}
			target := repo
			if tt.writePanic {
				upstream = source
				target = panicRepository{Repository: repo}
			}
			var event instrument.RefreshEvent
			refresher := instrument.NewRefresher(upstream, target, func() time.Time { return at.Add(time.Minute) }, func(e instrument.RefreshEvent) {
				event = e
				metrics.Observe(e)
			})

			assert.PanicsWithValue(t, tt.panicValue, func() { _ = refresher.Refresh(t.Context()) })

			assert.ErrorIs(t, event.Error, application.ErrInternal)
			after := metrics.Snapshot()[scope]
			assert.Equal(t, before.RefreshTotal, after.RefreshTotal)
			assert.Equal(t, before.LastSuccessfulFetch, after.LastSuccessfulFetch)
			assert.Equal(t, before.SnapshotSize, after.SnapshotSize)
			assert.Equal(t, uint64(1), after.RefreshErrorsTotal)
			ready, err := repo.HasSnapshot(t.Context(), scope)
			require.NoError(t, err)
			assert.Equal(t, tt.seeded, ready)
			if tt.seeded {
				rows, err := repo.List(t.Context(), instrument.Filter{SnapshotFilter: application.SnapshotFilter{Scopes: []application.Scope{scope}}})
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, "BTCUSDT", rows[0].Symbol)
				assert.Equal(t, at, rows[0].UpdatedAt)
			}
		})
	}
}
