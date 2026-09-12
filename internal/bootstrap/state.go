package bootstrap

import (
	"fmt"
	"sync/atomic"
	"time"

	"market-data/internal/application/instrument"
	"market-data/internal/application/kline"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/infrastructure/storage/memory"
)

// localState owns process storage. Feature services will share these repositories.
type localState struct {
	ready       atomic.Bool
	exchanges   *exchangeClients
	instruments instrument.Repository
	tickers     ticker.Repository
	marketStats marketstats.Repository
	klines      kline.Repository
}

func newLocalState(historyCandles int64, now func() time.Time) (*localState, error) {
	klines, err := memory.NewKlineRepository(historyCandles, now)
	if err != nil {
		return nil, fmt.Errorf("initialize candle storage: %w", err)
	}
	return &localState{
		instruments: memory.NewInstrumentRepository(),
		tickers:     memory.NewTickerRepository(),
		marketStats: memory.NewMarketStatsRepository(),
		klines:      klines,
	}, nil
}
