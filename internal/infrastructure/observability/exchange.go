package observability

import (
	"sync"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
)

type DurationStats struct {
	Count uint64  `json:"count"`
	Sum   float64 `json:"sum_seconds"`
	Max   float64 `json:"max_seconds"`
}

func (s *DurationStats) add(duration time.Duration) {
	seconds := max(0, duration.Seconds())
	s.Count++
	s.Sum += seconds
	s.Max = max(s.Max, seconds)
}

type ExchangeScope struct {
	application.Scope
	Operation string
}

type ExchangeStats struct {
	Requests        uint64
	Errors          uint64
	OversizedBodies uint64
	Duration        DurationStats
}

type Exchanges struct {
	mu     sync.Mutex
	scopes map[ExchangeScope]ExchangeStats
}

func NewExchanges() *Exchanges {
	return &Exchanges{scopes: make(map[ExchangeScope]ExchangeStats)}
}

func exchangeScope(event upstream.Event) ExchangeScope {
	key := ExchangeScope{Scope: application.Scope{Exchange: domain.ExchangeBybit, Market: event.Market}, Operation: operationName(event.Operation)}
	if event.Scope != upstream.Bybit {
		key.Exchange = domain.ExchangeBinance
		key.Market = domain.MarketSpot
		if event.Scope == upstream.BinanceLinear {
			key.Market = domain.MarketLinear
		}
	}
	return key
}

func (s *Exchanges) Observe(event upstream.Event) {
	key := exchangeScope(event)
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.scopes[key]
	stats.Requests++
	if event.Error != nil {
		stats.Errors++
	}
	if event.FailureReason == "oversized_body" {
		stats.OversizedBodies++
	}
	stats.Duration.add(event.Duration)
	s.scopes[key] = stats
}

func (s *Exchanges) Snapshot() map[ExchangeScope]ExchangeStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[ExchangeScope]ExchangeStats, len(s.scopes))
	for key, value := range s.scopes {
		result[key] = value
	}
	return result
}

func operationName(operation upstream.Operation) string {
	switch operation {
	case upstream.Tickers:
		return "tickers"
	case upstream.Klines:
		return "klines"
	case upstream.Instruments:
		return "instruments"
	case upstream.MarketStats:
		return "market_stats"
	default:
		return "unknown"
	}
}
