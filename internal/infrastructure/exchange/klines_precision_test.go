package exchange_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBybitCandleNumbersBeyondFloatRange(t *testing.T) {
	cases := []struct {
		name, raw, expected, extra string
		valid                      bool
	}{
		{"above float range", "1e309", "1" + strings.Repeat("0", 309), "null", true},
		{"maximum decimal expansion", "1e1023", "1" + strings.Repeat("0", 1023), "null", true},
		{"below float range", "1e-1022", "0." + strings.Repeat("0", 1021) + "1", "null", true},
		{"unused numeric overflow", "2", "2", "1e1000000", true},
		{"zero with large exponent", "0e309", "0", "null", true},
		{"positive expansion overflow", "1e1024", "", "null", false},
		{"negative expansion overflow", "1e-1023", "", "null", false},
		{"negative value", "-1e309", "", "null", false},
		{"malformed number", "1e", "", "null", false},
	}

	for _, market := range []domain.Market{domain.MarketSpot, domain.MarketLinear} {
		for _, tt := range cases {
			t.Run(string(market)+"/"+tt.name, func(t *testing.T) {
				scope := application.Scope{Exchange: domain.ExchangeBybit, Market: market}
				request := minuteRequest(scope, 1)
				// The unused field must not constrain the numeric range of used fields.
				row := fmt.Sprintf(`["%d",%s,%s,%s,%s,%s,%s,%s]`, request.From.UnixMilli(), tt.raw, tt.raw, tt.raw, tt.raw, tt.raw, tt.raw, tt.extra)
				body := fmt.Sprintf(`{"retCode":0,"result":{"category":%q,"symbol":"BTCUSDT","list":[%s]}}`, market, row)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				var events []upstream.Event
				provider, admission, id := candleProvider(t, scope, server, config.Defaults(), upstream.SystemClock{}, func(event upstream.Event) {
					events = append(events, event)
				})
				ctx, cancel, err := admission.Begin(t.Context(), id, upstream.Klines)
				require.NoError(t, err)
				defer cancel()

				rows, err := provider.GetKlines(ctx, request)

				assert.Equal(t, 1, admission.Attempts(ctx))
				require.Len(t, events, 1)
				if !tt.valid {
					assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
					assert.Nil(t, rows)
					return
				}

				require.NoError(t, err)
				require.Len(t, rows, 1)
				candle := rows[0].Candle
				assert.Equal(t, tt.expected, candle.Open.String())
				assert.Equal(t, tt.expected, candle.High.String())
				assert.Equal(t, tt.expected, candle.Low.String())
				assert.Equal(t, tt.expected, candle.Close.String())
				assert.Equal(t, tt.expected, candle.Volume.String())
				assert.Equal(t, tt.expected, candle.Turnover.String())
				assert.Nil(t, candle.TradesCount)
				assert.Equal(t, events[0].StartedAt.UTC(), rows[0].RequestStartedAt)
				assert.Equal(t, events[0].StartedAt.Add(events[0].Duration).UTC(), candle.FetchedAt)
			})
		}
	}
}
