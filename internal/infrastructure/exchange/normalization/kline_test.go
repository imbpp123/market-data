package normalization

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func candlePage(t *testing.T, exchange domain.Exchange) (kline.Request, [][]json.RawMessage, upstream.Response) {
	t.Helper()
	from := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	request := kline.Request{Query: kline.Query{Series: kline.Series{Scope: application.Scope{Exchange: exchange, Market: domain.MarketSpot}, Symbol: "BTCUSDT", Interval: domain.Timeframe1m}, From: from, To: from.Add(3 * time.Minute)}, Limit: 3}
	body := `[[1789171200000,"2","3","1","2.5","4",1789171259999,"10",0]]`
	if exchange == domain.ExchangeBybit {
		body = `[["1789171200000","2","3","1","2.5","4","10"]]`
	}
	var sources [][]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(body), &sources))
	response := upstream.Response{StartedAt: from.Add(30 * time.Second), FetchedAt: from.Add(90 * time.Second)}
	return request, sources, response
}

func TestKlineUsedDecimalsRejectWholePage(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"null", `null`}, {"empty", `""`}, {"invalid", `"no"`}, {"negative", `"-1"`},
		{"boolean", `true`}, {"array", `[]`}, {"object", `{}`},
		{"huge exponent", `"1e100000000"`}, {"tiny exponent", `"1e-100000000"`},
		{"long input", strconv.Quote(strings.Repeat("1", 1025))},
	}
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit} {
		for _, position := range []int{1, 2, 3, 4, 5, 6, 7} {
			if (exchange == domain.ExchangeBinance && position == 6) || (exchange == domain.ExchangeBybit && position == 7) {
				continue
			}
			for _, tt := range cases {
				t.Run(string(exchange)+"/"+strconv.Itoa(position)+"/"+tt.name, func(t *testing.T) {
					request, sources, response := candlePage(t, exchange)
					bad := append([]json.RawMessage(nil), sources[0]...)
					if exchange == domain.ExchangeBybit {
						bad[0] = json.RawMessage(`"1789171260000"`)
					} else {
						bad[0] = json.RawMessage(`1789171260000`)
						bad[6] = json.RawMessage(`1789171319999`)
					}
					bad[position] = json.RawMessage(tt.raw)
					sources = append(sources, bad)

					rows, err := Klines(t.Context(), request, sources, response)

					assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
					assert.Nil(t, rows)
				})
			}
		}
	}
}

func TestKlineIntegerContracts(t *testing.T) {
	cases := []struct {
		name     string
		exchange domain.Exchange
		position int
		raw      string
		valid    bool
		count    int64
	}{
		{"zero count", domain.ExchangeBinance, 8, `0`, true, 0},
		{"exact count", domain.ExchangeBinance, 8, `9007199254740993`, true, 9007199254740993},
		{"maximum count", domain.ExchangeBinance, 8, `9223372036854775807`, true, 9223372036854775807},
		{"fractional count", domain.ExchangeBinance, 8, `1.5`, false, 0},
		{"integral float count", domain.ExchangeBinance, 8, `1.0`, false, 0},
		{"exponent count", domain.ExchangeBinance, 8, `1e2`, false, 0},
		{"negative count", domain.ExchangeBinance, 8, `-1`, false, 0},
		{"overflow count", domain.ExchangeBinance, 8, `9223372036854775808`, false, 0},
		{"null count", domain.ExchangeBinance, 8, `null`, false, 0},
		{"string count", domain.ExchangeBinance, 8, `"1"`, false, 0},
		{"null open", domain.ExchangeBinance, 0, `null`, false, 0},
		{"quoted open", domain.ExchangeBinance, 0, `"1789171200000"`, false, 0},
		{"fractional open", domain.ExchangeBinance, 0, `1789171200000.5`, false, 0},
		{"negative open", domain.ExchangeBinance, 0, `-1`, false, 0},
		{"overflow open", domain.ExchangeBinance, 0, `9223372036854775808`, false, 0},
		{"large exact open", domain.ExchangeBinance, 0, `9007199254740993`, false, 0},
		{"seconds", domain.ExchangeBinance, 0, `1789171200`, false, 0},
		{"microseconds", domain.ExchangeBinance, 0, `1789171200000000`, false, 0},
		{"unaligned open", domain.ExchangeBinance, 0, `1789171200001`, false, 0},
		{"null close", domain.ExchangeBinance, 6, `null`, false, 0},
		{"close addition overflow", domain.ExchangeBinance, 6, `9223372036854775807`, false, 0},
		{"wrong close", domain.ExchangeBinance, 6, `1789171260000`, false, 0},
		{"close before open", domain.ExchangeBinance, 6, `1789171199999`, false, 0},
		{"null Bybit time", domain.ExchangeBybit, 0, `null`, false, 0},
		{"empty Bybit time", domain.ExchangeBybit, 0, `""`, false, 0},
		{"numeric Bybit time", domain.ExchangeBybit, 0, `1789171200000`, false, 0},
		{"signed Bybit time", domain.ExchangeBybit, 0, `"+1789171200000"`, false, 0},
		{"fractional Bybit time", domain.ExchangeBybit, 0, `"1789171200000.0"`, false, 0},
		{"negative Bybit time", domain.ExchangeBybit, 0, `"-1"`, false, 0},
		{"overflow Bybit time", domain.ExchangeBybit, 0, `"9223372036854775808"`, false, 0},
		{"Bybit seconds", domain.ExchangeBybit, 0, `"1789171200"`, false, 0},
		{"Bybit microseconds", domain.ExchangeBybit, 0, `"1789171200000000"`, false, 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			request, sources, response := candlePage(t, tt.exchange)
			sources[0][tt.position] = json.RawMessage(tt.raw)

			rows, err := Klines(t.Context(), request, sources, response)

			if !tt.valid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				assert.Nil(t, rows)
				return
			}
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.NotNil(t, rows[0].Candle.TradesCount)
			assert.Equal(t, tt.count, *rows[0].Candle.TradesCount)
		})
	}
}

func TestKlineOHLCBounds(t *testing.T) {
	cases := []struct {
		name     string
		position int
		value    string
	}{
		{"open below low", 1, `"0"`}, {"open above high", 1, `"4"`},
		{"close below low", 4, `"0"`}, {"close above high", 4, `"4"`},
		{"low above high", 3, `"4"`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			request, sources, response := candlePage(t, domain.ExchangeBinance)
			sources[0][tt.position] = json.RawMessage(tt.value)

			rows, err := Klines(t.Context(), request, sources, response)

			assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
			assert.Nil(t, rows)
		})
	}
}

func TestKlineExactNumbersZerosAndUnusedFields(t *testing.T) {
	cases := []struct {
		name, price string
		want        string
	}{
		{"long string", `"12345678901234567890.1234567890123456789"`, "12345678901234567890.1234567890123456789"},
		{"original JSON number", `12345678901234567890.1234567890123456789`, "12345678901234567890.1234567890123456789"},
		{"scientific notation", `"1.234567890123456789e-3"`, "0.001234567890123456789"},
		{"zero", `"0"`, "0"},
	}
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit} {
		for _, tt := range cases {
			t.Run(string(exchange)+"/"+tt.name, func(t *testing.T) {
				request, sources, response := candlePage(t, exchange)
				for _, i := range []int{1, 2, 3, 4, 5} {
					sources[0][i] = json.RawMessage(tt.price)
				}
				turnover := 7
				if exchange == domain.ExchangeBybit {
					turnover = 6
				}
				sources[0][turnover] = json.RawMessage(tt.price)
				sources[0] = append(sources[0], json.RawMessage(`{"ignored":true}`), json.RawMessage(`null`))

				rows, err := Klines(t.Context(), request, sources, response)

				require.NoError(t, err)
				require.Len(t, rows, 1)
				row := rows[0]
				assert.Equal(t, tt.want, row.Candle.Open.String())
				assert.Equal(t, tt.want, row.Candle.High.String())
				assert.Equal(t, tt.want, row.Candle.Low.String())
				assert.Equal(t, tt.want, row.Candle.Close.String())
				assert.Equal(t, tt.want, row.Candle.Volume.String())
				assert.Equal(t, tt.want, row.Candle.Turnover.String())
				assert.Equal(t, response.StartedAt, row.RequestStartedAt)
				assert.Equal(t, response.FetchedAt, row.Candle.FetchedAt)
				if exchange == domain.ExchangeBybit {
					assert.Nil(t, row.Candle.TradesCount)
				}
			})
		}
	}
}

func TestKlinePageValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*kline.Request, *[][]json.RawMessage, *upstream.Response)
	}{
		{"missing count", func(_ *kline.Request, rows *[][]json.RawMessage, _ *upstream.Response) { (*rows)[0] = (*rows)[0][:8] }},
		{"empty row", func(_ *kline.Request, rows *[][]json.RawMessage, _ *upstream.Response) { (*rows)[0] = nil }},
		{"duplicate", func(_ *kline.Request, rows *[][]json.RawMessage, _ *upstream.Response) {
			*rows = append(*rows, (*rows)[0])
		}},
		{"too many rows", func(r *kline.Request, rows *[][]json.RawMessage, _ *upstream.Response) {
			r.Limit = 1
			*rows = append(*rows, (*rows)[0])
		}},
		{"outside range", func(r *kline.Request, _ *[][]json.RawMessage, _ *upstream.Response) { r.To = r.From }},
		{"missing start evidence", func(_ *kline.Request, _ *[][]json.RawMessage, r *upstream.Response) { r.StartedAt = time.Time{} }},
		{"missing receipt", func(_ *kline.Request, _ *[][]json.RawMessage, r *upstream.Response) { r.FetchedAt = time.Time{} }},
		{"receipt before start", func(_ *kline.Request, _ *[][]json.RawMessage, r *upstream.Response) {
			r.FetchedAt = r.StartedAt.Add(-time.Second)
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			request, sources, response := candlePage(t, domain.ExchangeBinance)
			tt.change(&request, &sources, &response)

			rows, err := Klines(t.Context(), request, sources, response)

			assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
			assert.Nil(t, rows)
		})
	}
}

func TestKlineNormalizationHonorsCancellation(t *testing.T) {
	request, sources, response := candlePage(t, domain.ExchangeBybit)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	rows, err := Klines(ctx, request, sources, response)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, rows)
}
