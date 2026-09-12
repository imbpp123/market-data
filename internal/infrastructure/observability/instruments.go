package observability

import (
	"sync"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
)

type InstrumentStats struct {
	RefreshTotal        uint64
	RefreshErrorsTotal  uint64
	LastSuccessfulFetch time.Time
	SnapshotSize        int
}

// Instruments retains publication statistics independently of metrics exporters.
type Instruments struct {
	mu     sync.Mutex
	scopes map[application.Scope]InstrumentStats
}

func NewInstruments() *Instruments {
	return &Instruments{scopes: make(map[application.Scope]InstrumentStats)}
}

func (s *Instruments) Observe(event instrument.RefreshEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats := s.scopes[event.Scope]
	if event.Error != nil {
		stats.RefreshErrorsTotal++
	} else {
		stats.RefreshTotal++
		stats.LastSuccessfulFetch = event.UpdatedAt
		stats.SnapshotSize = event.Size
	}

	s.scopes[event.Scope] = stats
}

func (s *Instruments) Snapshot() map[application.Scope]InstrumentStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make(map[application.Scope]InstrumentStats, len(s.scopes))
	for scope, stats := range s.scopes {
		result[scope] = stats
	}

	return result
}
