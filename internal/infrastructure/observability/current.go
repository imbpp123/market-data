package observability

import (
	"sync"
	"time"

	"market-data/internal/application"
)

type CurrentScope struct {
	application.Scope
	Window time.Duration
}

type CurrentStats struct {
	RefreshTotal        uint64
	RefreshErrorsTotal  uint64
	LastSuccessfulFetch time.Time
	SnapshotSize        int
}

type Current struct {
	mu     sync.Mutex
	scopes map[CurrentScope]CurrentStats
}

func NewCurrent() *Current {
	return &Current{scopes: make(map[CurrentScope]CurrentStats)}
}

func (s *Current) Observe(event application.RefreshEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := CurrentScope{Scope: event.Scope, Window: event.Window}
	stats := s.scopes[key]
	if event.Error != nil {
		stats.RefreshErrorsTotal++
	} else {
		stats.RefreshTotal++
		stats.LastSuccessfulFetch = event.FetchedAt
		stats.SnapshotSize = event.Size
	}

	s.scopes[key] = stats
}

func (s *Current) Snapshot() map[CurrentScope]CurrentStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[CurrentScope]CurrentStats, len(s.scopes))
	for key, value := range s.scopes {
		result[key] = value
	}

	return result
}
