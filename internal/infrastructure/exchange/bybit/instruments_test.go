package bybit

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const linearInstrument = `{"symbol":"1000ABCUSDT","baseCoin":"1000ABC","quoteCoin":"USDT","status":"Trading","contractType":"LinearPerpetual","fundingInterval":480,"deliveryTime":"1700000000123","priceScale":"1","priceFilter":{"tickSize":"0.000000000000000123"},"lotSizeFilter":{"qtyStep":"0.000001","minOrderQty":"0.000002","maxOrderQty":"123.1234567890123456789","maxMktOrderQty":"99","postOnlyMaxOrderQty":"999","minNotionalValue":"5"}}`
const spotInstrument = `{"symbol":"ABCUSDT","baseCoin":"ABC","quoteCoin":"USDT","status":"Trading","priceFilter":{"tickSize":"0.01"},"lotSizeFilter":{"basePrecision":"0.000001","qtyStep":"99","minOrderQty":"invalid deprecated","maxOrderQty":"invalid deprecated","maxOrderAmt":"invalid deprecated","maxLimitOrderQty":"100","maxMarketOrderQty":"999","minOrderAmt":"5"}}`

func TestInstrumentStatuses(t *testing.T) {
	cases := []struct {
		value string
		want  domain.InstrumentStatus
	}{
		{"PreLaunch", domain.InstrumentStatusPreLaunch}, {"PendingOpen", domain.InstrumentStatusPreLaunch}, {"Trading", domain.InstrumentStatusTrading},
		{"Delivering", domain.InstrumentStatusSettling}, {"Closed", domain.InstrumentStatusClosed}, {"", domain.InstrumentStatusUnknown}, {"NEW", domain.InstrumentStatusUnknown},
	}
	for _, tt := range cases {
		t.Run(tt.value, func(t *testing.T) { assert.Equal(t, tt.want, instrumentStatus(tt.value)) })
	}
}

func TestNormalizeLinearInstrument(t *testing.T) {
	var source instrumentRow
	require.NoError(t, json.Unmarshal([]byte(linearInstrument), &source))

	row, err := normalizeLinearInstrument(source)

	require.NoError(t, err)
	assert.Equal(t, "1000ABCUSDT", row.Symbol)
	assert.Equal(t, "1000ABC", row.BaseAsset)
	assert.Equal(t, "0.000000000000000123", row.PriceTick.String())
	assert.Equal(t, "0.000001", row.QtyStep.String())
	require.NotNil(t, row.MinQty)
	assert.Equal(t, "0.000002", row.MinQty.String())
	require.NotNil(t, row.MaxQty)
	assert.Equal(t, "123.1234567890123456789", row.MaxQty.String())
	require.NotNil(t, row.MinNotional)
	assert.Equal(t, "5", row.MinNotional.String())
	require.NotNil(t, row.FundingInterval)
	assert.Equal(t, 8*time.Hour, *row.FundingInterval)
	require.NotNil(t, row.DelistingTime)
	assert.Equal(t, "2023-11-14T22:13:20.123Z", row.DelistingTime.Format(time.RFC3339Nano))
}

func TestNormalizeSpotIgnoresDeprecatedAndMarketLimits(t *testing.T) {
	var source instrumentRow
	require.NoError(t, json.Unmarshal([]byte(spotInstrument), &source))

	row, err := normalizeSpotInstrument(source)

	require.NoError(t, err)
	assert.Equal(t, "0.000001", row.QtyStep.String())
	assert.Nil(t, row.MinQty)
	require.NotNil(t, row.MaxQty)
	assert.Equal(t, "100", row.MaxQty.String())
	require.NotNil(t, row.MinNotional)
	assert.Equal(t, "5", row.MinNotional.String())
	assert.Nil(t, row.FundingInterval)
	assert.Nil(t, row.DelistingTime)
}

func TestInstrumentMetadata(t *testing.T) {
	cases := []struct {
		name, contract, funding, delivery string
		interval                          bool
		invalid                           bool
	}{
		{"zero delivery", "LinearPerpetual", "480", "0", true, false},
		{"missing metadata", "LinearPerpetual", "", "", false, false},
		{"expiry", "LinearFutures", "0", "1700000000123", false, false},
		{"unknown type", "NewContract", "480", "1700000000123", false, false},
		{"inverse", "InversePerpetual", "480", "0", false, true},
		{"zero funding", "LinearPerpetual", "0", "0", false, true},
		{"invalid funding", "LinearPerpetual", "bad", "0", false, true},
		{"negative delivery", "LinearPerpetual", "480", "-1", false, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var source instrumentRow
			require.NoError(t, json.Unmarshal([]byte(linearInstrument), &source))
			source.ContractType, source.FundingInterval, source.DeliveryTime = tt.contract, json.Number(tt.funding), tt.delivery

			row, err := normalizeLinearInstrument(source)

			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}
			require.NoError(t, err)
			assert.Nil(t, row.DelistingTime)
			if tt.interval {
				require.NotNil(t, row.FundingInterval)
				assert.Equal(t, 8*time.Hour, *row.FundingInterval)
			} else {
				assert.Nil(t, row.FundingInterval)
			}
		})
	}
}

func TestInstrumentInvalidLimits(t *testing.T) {
	cases := []struct {
		name   string
		change func(*instrumentRow)
	}{
		{"missing tick", func(r *instrumentRow) { r.PriceFilter.TickSize = "" }},
		{"zero tick", func(r *instrumentRow) { r.PriceFilter.TickSize = "0" }},
		{"negative tick", func(r *instrumentRow) { r.PriceFilter.TickSize = "-1" }},
		{"missing step", func(r *instrumentRow) { r.LotSizeFilter.QtyStep = "" }},
		{"zero step", func(r *instrumentRow) { r.LotSizeFilter.QtyStep = "0" }},
		{"negative step", func(r *instrumentRow) { r.LotSizeFilter.QtyStep = "-1" }},
		{"negative minimum", func(r *instrumentRow) { r.LotSizeFilter.MinOrderQty = "-1" }},
		{"invalid maximum", func(r *instrumentRow) { r.LotSizeFilter.MaxOrderQty = "bad" }},
		{"negative notional", func(r *instrumentRow) { r.LotSizeFilter.MinNotionalValue = "-1" }},
		{"conflicting bounds", func(r *instrumentRow) { r.LotSizeFilter.MaxOrderQty = "0.000001" }},
		{"empty symbol", func(r *instrumentRow) { r.Symbol = "" }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var source instrumentRow
			require.NoError(t, json.Unmarshal([]byte(linearInstrument), &source))
			tt.change(&source)

			_, err := normalizeLinearInstrument(source)

			assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
		})
	}
}

func instrumentEnvelope(market, rows, cursor string) string {
	return `{"retCode":0,"result":{"category":"` + market + `","list":[` + rows + `],"nextPageCursor":"` + cursor + `"}}`
}

func TestProviderCollectsAllPagesAndPreLaunch(t *testing.T) {
	requests := make([]string, 0)
	second := strings.ReplaceAll(linearInstrument, "1000ABC", "2000XYZ")
	prelaunch := strings.ReplaceAll(strings.ReplaceAll(linearInstrument, "1000ABC", "NEW"), "Trading", "PreLaunch")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v5/market/instruments-info", r.URL.Path)
		assert.Equal(t, "linear", r.URL.Query().Get("category"))
		assert.Equal(t, "1000", r.URL.Query().Get("limit"))
		assert.False(t, r.URL.Query().Has("baseCoin"))
		requests = append(requests, r.URL.Query().Get("status")+"/"+r.URL.Query().Get("cursor"))
		body := instrumentEnvelope("linear", linearInstrument, "next")
		if r.URL.Query().Get("cursor") == "next" {
			body = instrumentEnvelope("linear", second, "")
		}
		if r.URL.Query().Get("status") == "PreLaunch" {
			body = instrumentEnvelope("linear", prelaunch, "")
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	client, controller := testInstrumentClient(t, server)
	provider := NewLinearInstrumentProvider(client, nil)
	ctx, cancel, err := controller.Begin(t.Context(), upstream.Bybit, upstream.Instruments)
	require.NoError(t, err)
	defer cancel()

	rows, err := provider.GetInstruments(ctx)

	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, []string{"/", "/next", "PreLaunch/"}, requests)
	assert.Equal(t, "2000XYZUSDT", rows[1].Symbol)
	assert.Equal(t, domain.InstrumentStatusPreLaunch, rows[2].Status)
	assert.Equal(t, 3, controller.Attempts(ctx))
}

func TestProviderRejectsIncompleteCatalog(t *testing.T) {
	cases := []struct {
		name, first, second string
		status              int
		want                error
	}{
		{"failed page", instrumentEnvelope("linear", linearInstrument, "next"), `{}`, 400, application.ErrUpstream},
		{"repeated cursor", instrumentEnvelope("linear", linearInstrument, "next"), instrumentEnvelope("linear", strings.ReplaceAll(linearInstrument, "1000ABC", "NEW"), "next"), 200, application.ErrInvalidUpstreamData},
		{"no progress", instrumentEnvelope("linear", linearInstrument, "next"), instrumentEnvelope("linear", linearInstrument, "different"), 200, application.ErrInvalidUpstreamData},
		{"empty continued page", instrumentEnvelope("linear", linearInstrument, "next"), instrumentEnvelope("linear", "", ""), 200, application.ErrInvalidUpstreamData},
		{"conflicting duplicate", instrumentEnvelope("linear", linearInstrument, ""), instrumentEnvelope("linear", strings.Replace(linearInstrument, "Trading", "PreLaunch", 1), ""), 200, application.ErrInvalidUpstreamData},
		{"wrong category", instrumentEnvelope("spot", linearInstrument, ""), "", 200, application.ErrInvalidUpstreamData},
		{"missing list", `{"retCode":0,"result":{"category":"linear"}}`, "", 200, application.ErrInvalidUpstreamData},
		{"null list", `{"retCode":0,"result":{"category":"linear","list":null}}`, "", 200, application.ErrInvalidUpstreamData},
		{"invalid second row", instrumentEnvelope("linear", linearInstrument+`,{"symbol":"BAD"}`, ""), "", 200, application.ErrInvalidUpstreamData},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					_, _ = io.WriteString(w, tt.first)
					return
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.second)
			}))
			defer server.Close()
			client, controller := testInstrumentClient(t, server)
			provider := NewLinearInstrumentProvider(client, nil)
			ctx, cancel, err := controller.Begin(t.Context(), upstream.Bybit, upstream.Instruments)
			require.NoError(t, err)
			defer cancel()

			rows, err := provider.GetInstruments(ctx)

			assert.ErrorIs(t, err, tt.want)
			assert.Nil(t, rows)
		})
	}
}

func TestProviderDeduplicatesIdenticalCollections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, instrumentEnvelope("linear", linearInstrument, ""))
	}))
	defer server.Close()
	client, controller := testInstrumentClient(t, server)
	provider := NewLinearInstrumentProvider(client, nil)
	ctx, cancel, err := controller.Begin(t.Context(), upstream.Bybit, upstream.Instruments)
	require.NoError(t, err)
	defer cancel()

	rows, err := provider.GetInstruments(ctx)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "1000ABCUSDT", rows[0].Symbol)
	assert.Equal(t, 2, controller.Attempts(ctx))
}

func TestProviderSpotUsesOneDefaultRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "category=spot", r.URL.RawQuery)
		_, _ = io.WriteString(w, instrumentEnvelope("spot", spotInstrument, ""))
	}))
	defer server.Close()
	client, controller := testInstrumentClient(t, server)
	provider := NewSpotInstrumentProvider(client, nil)
	ctx, cancel, err := controller.Begin(t.Context(), upstream.Bybit, upstream.Instruments)
	require.NoError(t, err)
	defer cancel()

	rows, err := provider.GetInstruments(ctx)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "0.000001", rows[0].QtyStep.String())
	assert.Equal(t, 1, controller.Attempts(ctx))
}

func testInstrumentClient(t *testing.T, server *httptest.Server) (*Client, *upstream.Controller) {
	t.Helper()
	cfg := config.Defaults()
	controller, err := upstream.New(cfg, upstream.SystemClock{})
	require.NoError(t, err)
	transport, err := upstream.NewTransport(controller, upstream.Bybit, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
	require.NoError(t, err)
	client, err := NewClient(server.URL, transport)
	require.NoError(t, err)
	return client, controller
}

func TestProviderPagesShareOneAttemptLimit(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := strconv.FormatInt(calls.Add(1), 10)
		row := strings.ReplaceAll(linearInstrument, "1000ABC", "ABC"+page)
		_, _ = io.WriteString(w, instrumentEnvelope("linear", row, "page"+page))
	}))
	defer server.Close()
	cfg := config.Defaults()
	cfg.Upstream.LanesPerExchange.Instruments.MaxAttempts = 2
	controller, err := upstream.New(cfg, upstream.SystemClock{})
	require.NoError(t, err)
	transport, err := upstream.NewTransport(controller, upstream.Bybit, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
	require.NoError(t, err)
	client, err := NewClient(server.URL, transport)
	require.NoError(t, err)
	provider := NewLinearInstrumentProvider(client, nil)
	ctx, cancel, err := controller.Begin(t.Context(), upstream.Bybit, upstream.Instruments)
	require.NoError(t, err)
	defer cancel()

	rows, err := provider.GetInstruments(ctx)

	assert.ErrorIs(t, err, application.ErrUpstreamAttemptLimit)
	assert.Nil(t, rows)
	assert.Equal(t, int64(2), calls.Load())
}

func TestProviderLogsUnknownStatus(t *testing.T) {
	cases := []struct{ name, row, status string }{
		{"unknown", strings.Replace(spotInstrument, "Trading", "NewStatus", 1), "NewStatus"},
		{"missing", strings.Replace(spotInstrument, `"status":"Trading",`, "", 1), ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, instrumentEnvelope("spot", tt.row, ""))
			}))
			defer server.Close()
			client, controller := testInstrumentClient(t, server)
			provider := NewSpotInstrumentProvider(client, nil)
			var logs bytes.Buffer
			provider.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			ctx, cancel, err := controller.Begin(t.Context(), upstream.Bybit, upstream.Instruments)
			require.NoError(t, err)
			defer cancel()

			rows, err := provider.GetInstruments(ctx)

			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, domain.InstrumentStatusUnknown, rows[0].Status)
			var event map[string]any
			require.NoError(t, json.Unmarshal(logs.Bytes(), &event))
			assert.Equal(t, tt.status, event["status"])
			assert.Equal(t, "bybit", event["exchange"])
			assert.Equal(t, "ABCUSDT", event["symbol"])
		})
	}
}
