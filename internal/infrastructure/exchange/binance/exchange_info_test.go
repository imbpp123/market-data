package binance

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpotExchangeInfoLoadsCompleteCatalogWithoutPermissionSets(t *testing.T) {
	halted := strings.ReplaceAll(strings.ReplaceAll(spotInstrument, "1000ABC", "HALTED"), "TRADING", "HALT")
	cancelOnly := strings.ReplaceAll(strings.ReplaceAll(spotInstrument, "1000ABC", "CANCEL"), "TRADING", "CANCEL_ONLY")
	body := `{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":2000}],"symbols":[` + spotInstrument + `,` + halted + `,` + cancelOnly + `]}`
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v3/exchangeInfo", r.URL.Path)
		assert.Equal(t, url.Values{"showPermissionSets": {"false"}}, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	controller, client := spotCatalogClient(t, config.Defaults(), server)
	provider := NewSpotInstrumentProvider(client, nil)
	ctx, cancel, err := controller.Begin(t.Context(), upstream.BinanceSpot, upstream.Instruments)
	require.NoError(t, err)
	defer cancel()

	rows, err := provider.GetInstruments(ctx)

	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, 1, controller.Attempts(ctx))
	assert.Equal(t, []string{"1000ABCUSDT", "HALTEDUSDT", "CANCELUSDT"}, []string{rows[0].Symbol, rows[1].Symbol, rows[2].Symbol})
	assert.Equal(t, []domain.InstrumentStatus{domain.InstrumentStatusTrading, domain.InstrumentStatusHalted, domain.InstrumentStatusCancelOnly}, []domain.InstrumentStatus{rows[0].Status, rows[1].Status, rows[2].Status})
	for i, row := range rows {
		assert.Equal(t, domain.ExchangeBinance, row.Exchange)
		assert.Equal(t, domain.MarketSpot, row.Market)
		assert.Equal(t, []string{"1000ABC", "HALTED", "CANCEL"}[i], row.BaseAsset)
		assert.Equal(t, "USDT", row.QuoteAsset)
		assert.Equal(t, "0.000000000000000123", row.PriceTick.String())
		assert.Equal(t, "0.000001", row.QtyStep.String())
		require.NotNil(t, row.MinQty)
		assert.Equal(t, "0.000002", row.MinQty.String())
		require.NotNil(t, row.MaxQty)
		assert.Equal(t, "123.1234567890123456789", row.MaxQty.String())
		require.NotNil(t, row.MinNotional)
		assert.Equal(t, "10", row.MinNotional.String())
	}
}

func TestSpotExchangeInfoFailurePreservesPublishedSnapshot(t *testing.T) {
	valid := `{"rateLimits":[],"symbols":[` + spotInstrument + `]}`
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := io.WriteString(writer, valid)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	cases := []struct {
		name     string
		body     string
		encoding string
		maximum  int
		want     error
	}{
		{name: "plain over limit", body: valid, maximum: len(valid) - 1, want: application.ErrInvalidUpstreamData},
		{name: "gzip over decoded limit", body: compressed.String(), encoding: "gzip", maximum: len(valid) - 1, want: application.ErrInvalidUpstreamData},
		{name: "broken gzip", body: "broken", encoding: "gzip", maximum: len(valid), want: application.ErrUpstream},
		{name: "truncated gzip", body: compressed.String()[:compressed.Len()-4], encoding: "gzip", maximum: len(valid), want: application.ErrUpstream},
		{name: "truncated JSON", body: valid[:len(valid)-1], maximum: len(valid), want: application.ErrUpstreamUnavailable},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tt.encoding != "" {
					w.Header().Set("Content-Encoding", tt.encoding)
				}
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			cfg := config.Defaults()
			cfg.HTTPClient.MaxResponseBytes = tt.maximum
			cfg.HTTPClient.Retry.MaxAttempts = 1
			controller, client := spotCatalogClient(t, cfg, server)
			provider := NewSpotInstrumentProvider(client, nil)
			repository := memory.NewInstrumentRepository()
			previous := []domain.Instrument{{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot, Symbol: "PREVIOUS", BaseAsset: "PREVIOUS", QuoteAsset: "USDT", Status: domain.InstrumentStatusTrading, PriceTick: decimal.NewFromInt(1), QtyStep: decimal.NewFromInt(1), UpdatedAt: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)}}
			require.NoError(t, repository.ReplaceSnapshot(t.Context(), provider.Scope(), previous))
			refresher := instrument.NewRefresher(provider, repository, func() time.Time { return previous[0].UpdatedAt.Add(time.Hour) }, nil)
			ctx, cancel, err := controller.Begin(t.Context(), upstream.BinanceSpot, upstream.Instruments)
			require.NoError(t, err)
			defer cancel()

			err = refresher.Refresh(ctx)

			assert.ErrorIs(t, err, tt.want)
			rows, err := instrument.NewReader(repository, []application.Scope{provider.Scope()}).List(t.Context(), instrument.Query{})
			require.NoError(t, err)
			assert.Equal(t, previous, rows)
			assert.Equal(t, 1, controller.Attempts(ctx))
		})
	}
}

func spotCatalogClient(t *testing.T, cfg config.Config, server *httptest.Server) (*upstream.Controller, Client) {
	t.Helper()
	controller, err := upstream.New(cfg, upstream.SystemClock{})
	require.NoError(t, err)
	transport, err := upstream.NewTransport(controller, upstream.BinanceSpot, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
	require.NoError(t, err)
	client, err := NewClient(upstream.BinanceSpot, server.URL, transport)
	require.NoError(t, err)
	return controller, client
}
