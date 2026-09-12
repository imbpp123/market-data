package observability

import (
	"sync"
	"testing"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"

	"github.com/stretchr/testify/assert"
)

func TestKlineStatisticsCountBehaviorByScope(t *testing.T) {
	stats := NewKlines()
	spot := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
	linear := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}
	var workers sync.WaitGroup
	for range 50 {
		workers.Go(func() {
			stats.Observe(kline.Event{Scope: spot, CacheRead: true, CacheHit: true})
			stats.Observe(kline.Event{Scope: linear, CacheRead: true})
			stats.Observe(kline.Event{Scope: linear, SharedWait: true})
		})
	}
	workers.Wait()
	stats.Observe(kline.Event{Scope: linear, FillStarted: true})
	stats.Observe(kline.Event{Scope: linear, Attempts: 1})
	stats.Observe(kline.Event{Scope: linear, Attempts: 1})
	stats.Observe(kline.Event{Scope: linear, Downloaded: 7})

	snapshot := stats.Snapshot()

	assert.Equal(t, KlineStats{CacheHits: 50}, snapshot[spot])
	assert.Equal(t, KlineStats{CacheMisses: 50, SharedWaits: 50, Fills: 1, Attempts: 2, Downloaded: 7}, snapshot[linear])
	delete(snapshot, spot)
	assert.Contains(t, stats.Snapshot(), spot)
}
