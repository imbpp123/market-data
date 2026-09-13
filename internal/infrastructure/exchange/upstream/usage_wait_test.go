package upstream

import (
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageWaitResumesWithoutAnotherResponse(t *testing.T) {
	cases := []struct {
		name     string
		cooldown time.Duration
		resume   time.Duration
	}{
		{name: "observation expires", resume: 1200 * time.Millisecond},
		{name: "longer cooldown stays active", cooldown: 2 * time.Second, resume: 2 * time.Second},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				calls := 0
				base := &recorder{handle: func(*http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						time.Sleep(200 * time.Millisecond)
						return response(200, http.Header{"X-Mbx-Used-Weight-1s": {"1800"}}, `[]`), nil
					}
					return response(200, nil, `[]`), nil
				}}
				c, transport := setup(t, config.Defaults(), BinanceSpot, base)
				require.NoError(t, c.updateCatalog(BinanceSpot, start, []byte(`{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"SECOND","intervalNum":1,"limit":2000}]}`)))
				ctx := begin(t, c, BinanceSpot, Tickers)
				_, err := send(ctx, transport, "/api/v3/ticker/price")
				require.NoError(t, err)
				if tt.cooldown > 0 {
					c.Cooldown(BinanceSpot, start.Add(tt.cooldown))
				}

				_, err = send(ctx, transport, "/api/v3/ticker/bookTicker")

				require.NoError(t, err)
				sent := base.sent()
				require.Len(t, sent, 2)
				assert.Equal(t, start.Add(tt.resume), sent[1].at)
				assert.Equal(t, 2, c.Attempts(ctx))
				assert.Equal(t, 4, currentUsage(c, BinanceSpot, "weight_1000000000").common)
				assert.Equal(t, 8, currentUsage(c, BinanceSpot, "request_weight_1m").common)
			})
		})
	}
}
