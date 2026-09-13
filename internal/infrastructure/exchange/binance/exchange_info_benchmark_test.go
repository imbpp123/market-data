package binance

import (
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	"market-data/internal/application/instrument"
	"market-data/internal/config"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/stretchr/testify/require"
)

// Replay a complete local response without exchange network access.
func BenchmarkSpotExchangeInfo(b *testing.B) {
	path := os.Getenv("MDS_SPOT_EXCHANGE_INFO_FILE")
	if path == "" {
		b.Skip("set MDS_SPOT_EXCHANGE_INFO_FILE to a complete Spot exchangeInfo response")
	}
	body, err := os.ReadFile(path)
	require.NoError(b, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	cfg := config.Defaults()
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()

	for b.Loop() {
		// Each sample starts with empty usage and a fresh snapshot repository.
		controller, err := upstream.New(cfg, upstream.SystemClock{})
		require.NoError(b, err)
		transport, err := upstream.NewTransport(controller, upstream.BinanceSpot, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
		require.NoError(b, err)
		client, err := NewClient(upstream.BinanceSpot, server.URL, transport)
		require.NoError(b, err)
		provider := NewSpotInstrumentProvider(client, nil)
		repository := memory.NewInstrumentRepository()
		refresher := instrument.NewRefresher(provider, repository, time.Now, nil)
		ctx, cancel, err := controller.Begin(b.Context(), upstream.BinanceSpot, upstream.Instruments)
		require.NoError(b, err)

		err = refresher.Refresh(ctx)
		cancel()

		require.NoError(b, err)
		require.Equal(b, 1, controller.Attempts(ctx))
		runtime.KeepAlive(repository)
	}
}
