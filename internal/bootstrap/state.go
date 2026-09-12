package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"market-data/internal/application/instrument"
	"market-data/internal/application/kline"
	"market-data/internal/application/marketstats"
	"market-data/internal/application/ticker"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/observability"
	"market-data/internal/infrastructure/storage/memory"
)

// telemetry keeps SDK and lifecycle behavior behind one bootstrap boundary.
type telemetry interface {
	Report(error, map[string]string)
	Panic(map[string]string)
	Trace(context.Context, string, map[string]string) func()
	ObserveExchange(upstream.Event)
	HTTP(http.Handler, map[string]http.Handler, *slog.Logger) http.Handler
	Flush(context.Context) bool
	Close()
}

// localState owns process storage. Feature services will share these repositories.
type localState struct {
	instrumentMetrics *observability.Instruments
	currentMetrics    *observability.Current
	klineMetrics      *observability.Klines
	exchangeMetrics   *observability.Exchanges
	telemetry         telemetry
	inventory         kline.Inventory
	ready             atomic.Bool
	exchanges         *exchangeClients
	instruments       instrument.Repository
	tickers           ticker.Repository
	marketStats       marketstats.Repository
	klines            kline.Repository
}

func newLocalState(historyCandles int64, now func() time.Time) (*localState, error) {
	klines, err := memory.NewKlineRepository(historyCandles, now)
	if err != nil {
		return nil, fmt.Errorf("initialize candle storage: %w", err)
	}
	return &localState{
		telemetry:         (*observability.Sentry)(nil),
		instruments:       memory.NewInstrumentRepository(),
		exchangeMetrics:   observability.NewExchanges(),
		inventory:         klines.(kline.Inventory),
		currentMetrics:    observability.NewCurrent(),
		klineMetrics:      observability.NewKlines(),
		instrumentMetrics: observability.NewInstruments(),
		tickers:           memory.NewTickerRepository(),
		marketStats:       memory.NewMarketStatsRepository(),
		klines:            klines,
	}, nil
}
