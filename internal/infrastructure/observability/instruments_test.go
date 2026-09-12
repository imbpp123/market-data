package observability

import (
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
)

func TestInstrumentMetricsFollowPublication(t *testing.T) {
	metrics := NewInstruments()
	scope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	metrics.Observe(instrument.RefreshEvent{Scope: scope, UpdatedAt: now, Size: 2})

	metrics.Observe(instrument.RefreshEvent{Scope: scope, Error: application.ErrUpstream})

	snapshot := metrics.Snapshot()
	assert.Equal(t, InstrumentStats{RefreshTotal: 1, RefreshErrorsTotal: 1, LastSuccessfulFetch: now, SnapshotSize: 2}, snapshot[scope])
	delete(snapshot, scope)
	assert.Contains(t, metrics.Snapshot(), scope)
}

func TestEmptySnapshotUpdatesMetrics(t *testing.T) {
	metrics := NewInstruments()
	scope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	metrics.Observe(instrument.RefreshEvent{Scope: scope, UpdatedAt: now, Size: 2})

	metrics.Observe(instrument.RefreshEvent{Scope: scope, UpdatedAt: now.Add(time.Minute)})

	assert.Equal(t, InstrumentStats{RefreshTotal: 2, LastSuccessfulFetch: now.Add(time.Minute)}, metrics.Snapshot()[scope])
}
