package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"market-data/internal/application/kline"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/testfixture/grpcapi"
)

// TestInstalledCompositionFixture exposes the production composition to isolated consumers.
func TestInstalledCompositionFixture(t *testing.T) {
	path := os.Getenv("MDS_COMPOSITION_MANIFEST")
	if path == "" {
		t.Skip("set MDS_COMPOSITION_MANIFEST for installed client acceptance")
	}
	root, cancel := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg := config.Defaults()
	cfg.Exchanges.Bybit.Enabled = false
	cfg.Exchanges.Binance.Markets = []string{"spot"}
	state, err := newLocalState(1000, time.Now)
	require.NoError(t, err)
	fixture, err := grpcapi.New(root, nil)
	require.NoError(t, err)
	defer func() { cancel(); fixture.Service.Wait() }()
	state.instruments, state.tickers, state.marketStats = fixture.Instruments, fixture.Tickers, fixture.Stats
	var attempts atomic.Int64
	state.exchanges, err = newExchangeClients(cfg, instrumentTransport(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		require.Equal(t, "B", r.URL.Query().Get("symbol"))
		from, err := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
		require.NoError(t, err)
		to, err := strconv.ParseInt(r.URL.Query().Get("endTime"), 10, 64)
		require.NoError(t, err)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(candleFixture(domain.ExchangeBinance, domain.MarketSpot, time.UnixMilli(from).UTC(), time.UnixMilli(to+1).UTC())))}, nil
	}), upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
	require.NoError(t, err)
	end := time.Now().UTC().Truncate(time.Minute)
	request := kline.Request{Query: kline.Query{Series: kline.Series{Scope: grpcapi.Scope, Symbol: "A", Interval: domain.Timeframe1m}, From: end.Add(-2 * time.Minute), To: end}}
	rows := grpcapi.Rows(request)
	for i := range rows {
		rows[i].RequestStartedAt = end
		rows[i].Candle.FetchedAt = end
	}
	require.NoError(t, state.klines.UpsertMany(t.Context(), rows))
	partial := rows[:1]
	partial[0].Candle.Symbol = "B"
	require.NoError(t, state.klines.UpsertMany(t.Context(), partial))
	data, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	operations, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	settings := configuredGRPC(cfg)
	listen := func(_ context.Context, _, address string) (net.Listener, error) {
		if address == settings.Address {
			return data, nil
		}
		return operations, nil
	}
	worker := func(ctx context.Context) error {
		body, err := json.Marshal(map[string]any{"address": data.Addr().String(), "operations": operations.Addr().String(), "from": end.Add(-2 * time.Minute).Unix(), "to": end.Unix()})
		if err != nil {
			return err
		}
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, body, 0600); err != nil {
			return err
		}
		if err := os.Rename(temporary, path); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	require.NoError(t, state.serveGRPC(root, cfg, testLogger(), settings, listen, worker))
	require.Equal(t, int64(1), attempts.Load(), "one missing page is shared; later Python calls stay cache-only")
}
