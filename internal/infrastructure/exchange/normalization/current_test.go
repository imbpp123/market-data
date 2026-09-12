package normalization

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rawRow(t *testing.T, body string) Row {
	t.Helper()
	var row Row
	require.NoError(t, json.Unmarshal([]byte(body), &row))
	return row
}

func TestQuotePairs(t *testing.T) {
	cases := []struct {
		name, body, price, size string
		invalid                 bool
	}{
		{"both absent", `{}`, "", "", false},
		{"both empty", `{"p":"","q":""}`, "", "", false},
		{"both zero", `{"p":"0","q":"0"}`, "", "", false},
		{"zero size", `{"p":"10","q":"0"}`, "", "", false},
		{"exact pair", `{"p":"12345678901234567890.123456789","q":0.000000000000000001}`, "12345678901234567890.123456789", "0.000000000000000001", false},
		{"crossed book allowed", `{"p":"10","q":"2"}`, "10", "2", false},
		{"no price", `{"q":"1"}`, "", "", true},
		{"no size", `{"p":"1"}`, "", "", true},
		{"zero price", `{"p":"0","q":"1"}`, "", "", true},
		{"missing price zero size", `{"q":"0"}`, "", "", true},
		{"negative price", `{"p":"-1","q":"0"}`, "", "", true},
		{"negative size", `{"p":"1","q":"-1"}`, "", "", true},
		{"bad price", `{"p":"bad","q":"1"}`, "", "", true},
		{"bad type", `{"p":true,"q":"1"}`, "", "", true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			source := rawRow(t, tt.body)

			price, size, err := source.Quote("p", "q")

			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}

			require.NoError(t, err)
			if tt.price == "" {
				assert.Nil(t, price)
				assert.Nil(t, size)
				return
			}

			require.NotNil(t, price)
			require.NotNil(t, size)
			assert.Equal(t, tt.price, price.String())
			assert.Equal(t, tt.size, size.String())
		})
	}
}

func TestCurrentDecimals(t *testing.T) {
	cases := []struct {
		name, raw, want           string
		required, signed, invalid bool
	}{
		{"required missing", `null`, "", true, false, true},
		{"required empty", `""`, "", true, false, true},
		{"optional empty", `""`, "", false, false, false},
		{"explicit zero", `"0"`, "0", true, false, false},
		{"negative last", `"-1"`, "", true, false, true},
		{"signed funding", `"-0.00000000000000000001"`, "-0.00000000000000000001", false, true, false},
		{"signed zero", `"0"`, "0", false, true, false},
		{"bare minus", `"-"`, "", false, true, true},
		{"double sign", `"-+1"`, "", false, true, true},
		{"bad signed", `"bad"`, "", false, true, true},
		{"signed expansion boundary", `"-1e1023"`, "", true, true, true},
		{"huge exponent", `"1e1000000"`, "", true, false, true},
		{"tiny exponent", `"-1e-1000000"`, "", true, true, true},
		{"zero huge exponent", `"0e1000000"`, "", true, false, true},
		{"long source", `"` + strings.Repeat("1", 1025) + `"`, "", true, false, true},
		{"scientific", `"-1.23e-8"`, "-0.0000000123", true, true, false},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			source := Row{"value": json.RawMessage(tt.raw)}

			value, err := source.Decimal("value", tt.required, tt.signed)

			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}

			require.NoError(t, err)
			if tt.want == "" {
				assert.Nil(t, value)
				return
			}

			require.NotNil(t, value)
			assert.Equal(t, tt.want, value.String())
		})
	}
}

func TestFundingApplicabilityAndTimestamps(t *testing.T) {
	cases := []struct {
		name       string
		market     domain.Market
		kind       domain.ContractType
		body, rate string
		next       int64
		invalid    bool
	}{
		{"spot ignores placeholders", domain.MarketSpot, domain.ContractTypeUnknown, `{"rate":{},"nextFundingTime":-1}`, "", 0, false},
		{"expiry ignores placeholders", domain.MarketLinear, domain.ContractTypeExpiry, `{"rate":{},"nextFundingTime":-1}`, "", 0, false},
		{"unknown unscheduled", domain.MarketLinear, domain.ContractTypeUnknown, `{"rate":"bad"}`, "", 0, false},
		{"known perpetual rate", domain.MarketLinear, domain.ContractTypePerpetual, `{"rate":"-0.001"}`, "-0.001", 0, false},
		{"unknown explicit schedule", domain.MarketLinear, domain.ContractTypeUnknown, `{"rate":"0","nextFundingTime":"1800000000123"}`, "0", 1800000000123, false},
		{"integer schedule no rate", domain.MarketLinear, domain.ContractTypePerpetual, `{"nextFundingTime":1800000000123}`, "", 1800000000123, false},
		{"zero schedule", domain.MarketLinear, domain.ContractTypePerpetual, `{"nextFundingTime":0}`, "", 0, false},
		{"negative schedule", domain.MarketLinear, domain.ContractTypeUnknown, `{"nextFundingTime":-1}`, "", 0, true},
		{"fractional schedule", domain.MarketLinear, domain.ContractTypeUnknown, `{"nextFundingTime":1.5}`, "", 0, true},
		{"invalid schedule", domain.MarketLinear, domain.ContractTypeUnknown, `{"nextFundingTime":"bad"}`, "", 0, true},
		{"overflow", domain.MarketLinear, domain.ContractTypeUnknown, `{"nextFundingTime":9223372036854775808}`, "", 0, true},
		{"unserializable year", domain.MarketLinear, domain.ContractTypeUnknown, `{"nextFundingTime":9223372036854775807}`, "", 0, true},
		{"invalid rate", domain.MarketLinear, domain.ContractTypePerpetual, `{"rate":"bad"}`, "", 0, true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			source := rawRow(t, tt.body)

			rate, next, err := source.Funding(tt.market, tt.kind, "rate")

			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}

			require.NoError(t, err)
			if tt.rate == "" {
				assert.Nil(t, rate)
			} else {
				require.NotNil(t, rate)
				assert.Equal(t, tt.rate, rate.String())
			}

			if tt.next == 0 {
				assert.Nil(t, next)
			} else {
				require.NotNil(t, next)
				assert.Equal(t, time.UnixMilli(tt.next).UTC(), *next)
			}
		})
	}
}

func TestBinanceTradeCount(t *testing.T) {
	cases := []struct {
		name, body string
		want       *int64
		invalid    bool
	}{
		{"missing", `{}`, nil, false},
		{"zero", `{"count":0}`, new(int64(0)), false},
		{"exact large integer", `{"count":9007199254740993}`, new(int64(9007199254740993)), false},
		{"maximum", `{"count":9223372036854775807}`, new(int64(9223372036854775807)), false},
		{"empty", `{"count":""}`, nil, true},
		{"null", `{"count":null}`, nil, true},
		{"fraction", `{"count":1.5}`, nil, true},
		{"negative", `{"count":-1}`, nil, true},
		{"overflow", `{"count":9223372036854775808}`, nil, true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			source := rawRow(t, tt.body)

			count, err := source.Count()

			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, count)
		})
	}
}

func TestBybitPriceChange(t *testing.T) {
	cases := []struct {
		name, previous, last, change string
		invalid                      bool
	}{
		{"rise", "100", "105.25", "5.25", false},
		{"fall", "100", "95", "-5", false},
		{"unchanged", "100", "100", "0", false},
		{"exact", "100.00000000000000000001", "100.00000000000000000002", "0.00000000000000000001", false},
		{"missing reference", "", "bad", "", false},
		{"zero reference", "0", "bad", "", false},
		{"negative reference", "-1", "10", "", true},
		{"invalid reference", "bad", "10", "", true},
		{"required last missing", "1", "", "", true},
		{"required last invalid", "1", "bad", "", true},
		{"required last negative", "1", "-1", "", true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			source := rawRow(t, `{"symbol":"A","highPrice24h":"200","lowPrice24h":"0","volume24h":"2","turnover24h":"301","price24hPcnt":"999"}`)
			source["prevPrice24h"], _ = json.Marshal(tt.previous)
			source["lastPrice"], _ = json.Marshal(tt.last)
			scope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketSpot}
			at := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)

			rows, err := Statistics(t.Context(), []Row{source}, scope, at)

			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}

			require.NoError(t, err)
			require.Len(t, rows, 1)
			if tt.change == "" {
				assert.Nil(t, rows[0].PriceChange)
			} else {
				require.NotNil(t, rows[0].PriceChange)
				assert.Equal(t, tt.change, rows[0].PriceChange.String())
			}

			assert.Nil(t, rows[0].TradeCount)
			assert.Equal(t, "301", rows[0].Turnover.String())
			assert.Equal(t, at, rows[0].FetchedAt)
		})
	}
}

func TestStatisticsRejectInvalidRequiredFields(t *testing.T) {
	for _, field := range []string{"highPrice", "lowPrice", "volume", "quoteVolume"} {
		t.Run(field, func(t *testing.T) {
			for _, raw := range []string{`null`, `""`, `"bad"`, `"-1"`, `true`, `"1e1000000"`} {
				t.Run(raw, func(t *testing.T) {
					row := rawRow(t, `{"symbol":"A","highPrice":"2","lowPrice":"1","volume":"3","quoteVolume":"4"}`)
					row[field] = json.RawMessage(raw)

					rows, err := Statistics(t.Context(), []Row{row}, application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}, time.Time{})

					assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
					assert.Nil(t, rows)
				})
			}
		})
	}
}

func TestStatisticsRejectDuplicatesAndCancellation(t *testing.T) {
	cases := []struct {
		name   string
		cancel bool
		rows   []Row
	}{
		{"duplicates", false, []Row{{"symbol": json.RawMessage(`"A"`)}, {"symbol": json.RawMessage(`"A"`)}}},
		{"missing symbol", false, []Row{{}}},
		{"numeric symbol", false, []Row{{"symbol": json.RawMessage(`123`)}}},
		{"cancellation", true, []Row{{"symbol": json.RawMessage(`"A"`)}}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancel {
				cancel()
			}

			rows, err := Statistics(ctx, tt.rows, application.Scope{Exchange: domain.ExchangeBinance}, time.Time{})

			if tt.cancel {
				assert.ErrorIs(t, err, context.Canceled)
			} else {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
			}

			assert.Nil(t, rows)
		})
	}
}

type contractStore struct {
	instrument.Repository
	err error
}

func (s contractStore) List(context.Context, instrument.Filter) ([]domain.Instrument, error) {
	return nil, s.err
}

func TestContractLookupIsOptionalAndScopeSpecific(t *testing.T) {
	cases := []struct {
		name    string
		ready   bool
		failure error
	}{
		{name: "unready catalog"}, {name: "ready catalog", ready: true}, {name: "repository failure", failure: application.ErrInternal}, {name: "cancellation", failure: context.Canceled},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			repo := memory.NewInstrumentRepository()
			scope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}
			if tt.ready {
				require.NoError(t, repo.ReplaceSnapshot(t.Context(), scope, []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "P", ContractType: domain.ContractTypePerpetual}, {Exchange: scope.Exchange, Market: scope.Market, Symbol: "F", ContractType: domain.ContractTypeExpiry}}))
				other := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketLinear}
				require.NoError(t, repo.ReplaceSnapshot(t.Context(), other, []domain.Instrument{{Exchange: other.Exchange, Market: other.Market, Symbol: "P", ContractType: domain.ContractTypeExpiry}}))
			}
			source := repo
			if tt.failure != nil {
				source = contractStore{Repository: repo, err: tt.failure}
			}

			contracts, err := Contracts(t.Context(), source, scope)

			if tt.failure != nil {
				assert.ErrorIs(t, err, tt.failure)
				return
			}
			require.NoError(t, err)
			if tt.ready {
				assert.Equal(t, map[string]domain.ContractType{"P": domain.ContractTypePerpetual, "F": domain.ContractTypeExpiry}, contracts)
			} else {
				assert.Empty(t, contracts)
			}
		})
	}
}
