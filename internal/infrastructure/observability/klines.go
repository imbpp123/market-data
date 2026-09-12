package observability

import (
	"sync"

	"market-data/internal/application"
	"market-data/internal/application/kline"
)

type KlineStats struct {
	CacheHits   uint64
	CacheMisses uint64
	Fills       uint64
	SharedWaits uint64
	Attempts    uint64
	Downloaded  uint64
	Duration    DurationStats
}

type Klines struct {
	mu     sync.Mutex
	scopes map[application.Scope]KlineStats
}

func NewKlines() *Klines {
	return &Klines{scopes: make(map[application.Scope]KlineStats)}
}

func (s *Klines) Observe(event kline.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.scopes[event.Scope]
	if event.CacheRead {
		if event.CacheHit {
			stats.CacheHits++
		} else {
			stats.CacheMisses++
		}
	}
	if event.FillStarted {
		stats.Fills++
	}
	if event.SharedWait {
		stats.SharedWaits++
	}
	stats.Attempts += uint64(event.Attempts)
	stats.Downloaded += uint64(event.Downloaded)
	if event.Completed {
		stats.Duration.add(event.Duration)
	}
	s.scopes[event.Scope] = stats
}

func (s *Klines) Snapshot() map[application.Scope]KlineStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[application.Scope]KlineStats, len(s.scopes))
	for scope, stats := range s.scopes {
		result[scope] = stats
	}
	return result
}
