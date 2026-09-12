package observability

import (
	"sync"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
)

func TestCurrentMetricsTrackIndependentPublications(t *testing.T) {
	stats := NewCurrent()
	scope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}
	at := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	stats.Observe(application.RefreshEvent{Scope: scope, Window: 24 * time.Hour, FetchedAt: at, Size: 3})
	var workers sync.WaitGroup
	for range 50 {
		workers.Go(func() {
			stats.Observe(application.RefreshEvent{Scope: scope, FetchedAt: at.Add(time.Second), Size: 2})
			stats.Observe(application.RefreshEvent{Scope: scope, Window: 24 * time.Hour, Error: application.ErrInvalidUpstreamData})
			_ = stats.Snapshot()
		})
	}

	workers.Wait()

	result := stats.Snapshot()

	assert.Equal(t, CurrentStats{RefreshTotal: 50, LastSuccessfulFetch: at.Add(time.Second), SnapshotSize: 2}, result[CurrentScope{Scope: scope}])
	assert.Equal(t, CurrentStats{RefreshTotal: 1, RefreshErrorsTotal: 50, LastSuccessfulFetch: at, SnapshotSize: 3}, result[CurrentScope{Scope: scope, Window: 24 * time.Hour}])
	delete(result, CurrentScope{Scope: scope})
	assert.Len(t, stats.Snapshot(), 2)
}
