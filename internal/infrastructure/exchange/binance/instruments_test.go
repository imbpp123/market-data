package binance

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const spotInstrument = `{"symbol":"1000ABCUSDT","baseAsset":"1000ABC","quoteAsset":"USDT","status":"TRADING","pricePrecision":1,"quantityPrecision":1,"filters":[{"filterType":"MARKET_LOT_SIZE","stepSize":"99","maxQty":"99"},{"filterType":"NOTIONAL","minNotional":"10","applyMinToMarket":false},{"filterType":"LOT_SIZE","stepSize":"0.000001","minQty":"0.000002","maxQty":"123.1234567890123456789"},{"filterType":"PRICE_FILTER","tickSize":"0.000000000000000123"},{"filterType":"MIN_NOTIONAL","minNotional":"5","applyToMarket":false}]}`

func TestInstrumentStatuses(t *testing.T) {
	cases := []struct {
		market domain.Market
		value  string
		want   domain.InstrumentStatus
	}{
		{domain.MarketSpot, "TRADING", domain.InstrumentStatusTrading}, {domain.MarketSpot, "END_OF_DAY", domain.InstrumentStatusHalted},
		{domain.MarketSpot, "HALT", domain.InstrumentStatusHalted}, {domain.MarketSpot, "BREAK", domain.InstrumentStatusHalted},
		{domain.MarketSpot, "CANCEL_ONLY", domain.InstrumentStatusCancelOnly}, {domain.MarketSpot, "", domain.InstrumentStatusUnknown},
		{domain.MarketSpot, "NEW", domain.InstrumentStatusUnknown}, {domain.MarketSpot, "CLOSE", domain.InstrumentStatusUnknown},
		{domain.MarketLinear, "PENDING_TRADING", domain.InstrumentStatusPreLaunch}, {domain.MarketLinear, "TRADING", domain.InstrumentStatusTrading},
		{domain.MarketLinear, "PRE_DELIVERING", domain.InstrumentStatusSettling}, {domain.MarketLinear, "DELIVERING", domain.InstrumentStatusSettling},
		{domain.MarketLinear, "PRE_SETTLE", domain.InstrumentStatusSettling}, {domain.MarketLinear, "SETTLING", domain.InstrumentStatusSettling},
		{domain.MarketLinear, "DELIVERED", domain.InstrumentStatusClosed}, {domain.MarketLinear, "CLOSE", domain.InstrumentStatusClosed},
		{domain.MarketLinear, "TRADING_HALT", domain.InstrumentStatusHalted}, {domain.MarketLinear, "TRADING_CANCEL_ONLY", domain.InstrumentStatusCancelOnly},
		{domain.MarketLinear, "", domain.InstrumentStatusUnknown}, {domain.MarketLinear, "NEW", domain.InstrumentStatusUnknown}, {domain.MarketLinear, "HALT", domain.InstrumentStatusUnknown},
	}
	for _, tt := range cases {
		t.Run(string(tt.market)+"/"+tt.value, func(t *testing.T) {
			mapStatus := spotInstrumentStatus
			if tt.market == domain.MarketLinear {
				mapStatus = linearInstrumentStatus
			}
			assert.Equal(t, tt.want, mapStatus(tt.value))
		})
	}
}

func TestNormalizeSpotInstrument(t *testing.T) {
	var source instrumentRow
	require.NoError(t, json.Unmarshal([]byte(spotInstrument), &source))

	row, err := normalizeSpotInstrument(source)

	require.NoError(t, err)
	assert.Equal(t, "1000ABCUSDT", row.Symbol)
	assert.Equal(t, "1000ABC", row.BaseAsset)
	assert.Equal(t, "USDT", row.QuoteAsset)
	assert.Equal(t, "0.000000000000000123", row.PriceTick.String())
	assert.Equal(t, "0.000001", row.QtyStep.String())
	require.NotNil(t, row.MinQty)
	assert.Equal(t, "0.000002", row.MinQty.String())
	require.NotNil(t, row.MaxQty)
	assert.Equal(t, "123.1234567890123456789", row.MaxQty.String())
	require.NotNil(t, row.MinNotional)
	assert.Equal(t, "10", row.MinNotional.String())
	assert.Nil(t, row.FundingInterval)
	assert.Nil(t, row.DelistingTime)
}

func TestInstrumentFilters(t *testing.T) {
	cases := []struct {
		name    string
		filters string
		want    string
		invalid bool
	}{
		{"no notional", `[]`, "", false},
		{"min notional", `[{"filterType":"MIN_NOTIONAL","minNotional":"5"}]`, "5", false},
		{"notional", `[{"filterType":"NOTIONAL","minNotional":"10"}]`, "10", false},
		{"larger old filter", `[{"filterType":"NOTIONAL","minNotional":"5"},{"filterType":"MIN_NOTIONAL","minNotional":"10"}]`, "10", false},
		{"negative notional", `[{"filterType":"NOTIONAL","minNotional":"-1"}]`, "", true},
		{"invalid notional", `[{"filterType":"MIN_NOTIONAL","minNotional":"invalid"}]`, "", true},
		{"duplicate filter", `[{"filterType":"NOTIONAL"},{"filterType":"NOTIONAL"}]`, "", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			source := instrumentRow{Symbol: "A", BaseAsset: "B", QuoteAsset: "Q"}
			require.NoError(t, json.Unmarshal([]byte(tt.filters), &source.Filters))
			source.Filters = append(source.Filters, instrumentFilter{Type: "PRICE_FILTER", TickSize: "0.1"}, instrumentFilter{Type: "LOT_SIZE", StepSize: "0.1"})

			row, err := normalizeSpotInstrument(source)

			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}
			require.NoError(t, err)
			if tt.want == "" {
				assert.Nil(t, row.MinNotional)
			} else {
				require.NotNil(t, row.MinNotional)
				assert.Equal(t, tt.want, row.MinNotional.String())
			}
			assert.Nil(t, row.MinQty)
			assert.Nil(t, row.MaxQty)
		})
	}
}

func TestInstrumentInvalidStepsAndBounds(t *testing.T) {
	cases := []struct {
		name                         string
		tick, step, minimum, maximum string
	}{
		{"missing tick", "", "1", "", ""}, {"zero tick", "0", "1", "", ""}, {"negative tick", "-1", "1", "", ""},
		{"missing step", "1", "", "", ""}, {"zero step", "1", "0", "", ""}, {"negative step", "1", "-1", "", ""},
		{"negative min", "1", "1", "-1", ""}, {"invalid max", "1", "1", "", "x"}, {"conflicting", "1", "1", "2", "1"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			source := instrumentRow{Symbol: "A", BaseAsset: "B", QuoteAsset: "Q", Filters: []instrumentFilter{{Type: "PRICE_FILTER", TickSize: tt.tick}, {Type: "LOT_SIZE", StepSize: tt.step, MinQty: tt.minimum, MaxQty: tt.maximum}}}

			_, err := normalizeSpotInstrument(source)

			assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
		})
	}
}

func TestSpotZeroQuantityLimitsStayExplicit(t *testing.T) {
	source := instrumentRow{Symbol: "A", BaseAsset: "B", QuoteAsset: "Q", Filters: []instrumentFilter{{Type: "PRICE_FILTER", TickSize: "1"}, {Type: "LOT_SIZE", StepSize: "1", MinQty: "0", MaxQty: "0"}}}

	row, err := normalizeSpotInstrument(source)

	require.NoError(t, err)
	require.NotNil(t, row.MinQty)
	assert.Equal(t, "0", row.MinQty.String())
	require.NotNil(t, row.MaxQty)
	assert.Equal(t, "0", row.MaxQty.String())
}

func TestFundingMetadata(t *testing.T) {
	cases := []struct {
		name, body string
		invalid    bool
	}{
		{"empty", `[]`, false}, {"explicit", `[{"symbol":"A","fundingIntervalHours":4}]`, false},
		{"missing array", `null`, true}, {"wrong envelope", `{}`, true}, {"missing symbol", `[{"fundingIntervalHours":4}]`, true},
		{"missing interval", `[{"symbol":"A"}]`, true}, {"zero", `[{"symbol":"A","fundingIntervalHours":0}]`, true},
		{"negative", `[{"symbol":"A","fundingIntervalHours":-1}]`, true}, {"fraction", `[{"symbol":"A","fundingIntervalHours":1.5}]`, true},
		{"overflow", `[{"symbol":"A","fundingIntervalHours":9223372036854775807}]`, true},
		{"duplicate", `[{"symbol":"A","fundingIntervalHours":4},{"symbol":"A","fundingIntervalHours":4}]`, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFunding([]byte(tt.body))
			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}
			require.NoError(t, err)
			if tt.name == "empty" {
				assert.Empty(t, got)
			} else {
				require.Contains(t, got, "A")
				assert.Equal(t, 4*time.Hour, *got["A"])
			}
		})
	}
}

func TestCapturedFundingMetadata(t *testing.T) {
	body, err := os.ReadFile("../../../../testdata/exchange/binance-funding-info.json")
	require.NoError(t, err)
	var fixture struct {
		RawResponse json.RawMessage `json:"raw_response"`
	}
	require.NoError(t, json.Unmarshal(body, &fixture))

	funding, err := parseFunding(fixture.RawResponse)

	require.NoError(t, err)
	require.Len(t, funding, 782)
	require.Contains(t, funding, "GTCUSDT")
	assert.Equal(t, 8*time.Hour, *funding["GTCUSDT"])
	require.Contains(t, funding, "LPTUSDT")
	assert.Equal(t, 4*time.Hour, *funding["LPTUSDT"])
	require.Contains(t, funding, "IOSTUSDT")
	assert.Equal(t, time.Hour, *funding["IOSTUSDT"])

}

func TestLinearProviderJoinsFunding(t *testing.T) {
	cases := []struct {
		name, contract, funding string
		want                    time.Duration
		invalid                 bool
	}{
		{"explicit", "PERPETUAL", `[{"symbol":"A","fundingIntervalHours":4}]`, 4 * time.Hour, false},
		{"absent", "PERPETUAL", `[]`, 0, false},
		{"expiry", "CURRENT_QUARTER", `[{"symbol":"A","fundingIntervalHours":4}]`, 0, false},
		{"unknown", "FUTURE_TYPE", `[{"symbol":"A","fundingIntervalHours":4}]`, 0, false},
		{"failed metadata", "PERPETUAL", `{}`, 0, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				assert.Empty(t, r.URL.RawQuery)
				if r.URL.Path == "/fapi/v1/fundingInfo" {
					_, _ = io.WriteString(w, tt.funding)
					return
				}
				assert.Equal(t, "/fapi/v1/exchangeInfo", r.URL.Path)
				_, _ = io.WriteString(w, `{"rateLimits":[],"symbols":[{"symbol":"A","baseAsset":"B","quoteAsset":"Q","contractType":"`+tt.contract+`","deliveryDate":4133404800000,"filters":[{"filterType":"PRICE_FILTER","tickSize":"0.1"},{"filterType":"LOT_SIZE","stepSize":"0.01"},{"filterType":"MIN_NOTIONAL","notional":"5","minNotional":"99"}]}]}`)
			}))
			defer server.Close()
			cfg := config.Defaults()
			controller, err := upstream.New(cfg, upstream.SystemClock{})
			require.NoError(t, err)
			transport, err := upstream.NewTransport(controller, upstream.BinanceLinear, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
			require.NoError(t, err)
			client, err := NewClient(upstream.BinanceLinear, server.URL, transport)
			require.NoError(t, err)
			provider := NewLinearInstrumentProvider(client, nil)
			ctx, cancel, err := controller.Begin(t.Context(), upstream.BinanceLinear, upstream.Instruments)
			require.NoError(t, err)
			defer cancel()

			rows, err := provider.GetInstruments(ctx)

			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				assert.Nil(t, rows)
				return
			}
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, "5", rows[0].MinNotional.String())
			assert.Nil(t, rows[0].DelistingTime)
			if tt.want == 0 {
				assert.Nil(t, rows[0].FundingInterval)
			} else {
				require.NotNil(t, rows[0].FundingInterval)
				assert.Equal(t, tt.want, *rows[0].FundingInterval)
			}
			assert.Equal(t, 2, controller.Attempts(ctx))
		})
	}
}

func TestProviderRejectsMalformedCatalog(t *testing.T) {
	cases := []struct{ name, body string }{
		{"missing symbols", `{"rateLimits":[]}`},
		{"null symbols", `{"rateLimits":[],"symbols":null}`},
		{"wrong symbols type", `{"rateLimits":[],"symbols":{}}`},
		{"duplicate symbols", `{"rateLimits":[],"symbols":[` + spotInstrument + `,` + spotInstrument + `]}`},
		{"invalid row after valid row", `{"rateLimits":[],"symbols":[` + spotInstrument + `,{"symbol":"BAD"}]}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			cfg := config.Defaults()
			controller, err := upstream.New(cfg, upstream.SystemClock{})
			require.NoError(t, err)
			transport, err := upstream.NewTransport(controller, upstream.BinanceSpot, server.Client().Transport, cfg, func(time.Duration) time.Duration { return 0 }, nil)
			require.NoError(t, err)
			client, err := NewClient(upstream.BinanceSpot, server.URL, transport)
			require.NoError(t, err)
			provider := NewSpotInstrumentProvider(client, nil)
			ctx, cancel, err := controller.Begin(t.Context(), upstream.BinanceSpot, upstream.Instruments)
			require.NoError(t, err)
			defer cancel()

			rows, err := provider.GetInstruments(ctx)

			assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
			assert.Nil(t, rows)
		})
	}
}
