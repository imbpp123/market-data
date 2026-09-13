package upstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type brokenResponseBody struct{}

func (brokenResponseBody) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func (brokenResponseBody) Close() error {
	return nil
}

func TestServerRetryAfterSurvivesBodyReadFailure(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{name: "internal server error", status: http.StatusInternalServerError},
		{name: "bad gateway", status: http.StatusBadGateway},
		{name: "service unavailable", status: http.StatusServiceUnavailable},
		{name: "gateway timeout", status: http.StatusGatewayTimeout},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				base := &recorder{handle: func(*http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						return &http.Response{
							StatusCode: tt.status,
							Header:     http.Header{"Retry-After": {"60"}},
							Body:       brokenResponseBody{},
						}, nil
					}

					return response(200, nil, `{"retCode":0}`), nil
				}}
				c, transport := setup(t, config.Defaults(), Bybit, base)
				ctx := begin(t, c, Bybit, Tickers)
				started := time.Now()

				_, err := send(ctx, transport, "/v5/market/tickers?category=spot")

				assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
				assert.Len(t, base.sent(), 1)
				assert.Equal(t, 1, c.Attempts(ctx))
				assert.Equal(t, started, time.Now())
			})
		})
	}
}

func TestUsageHeadersSurviveResponseFailure(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		brokenBody bool
	}{
		{name: "HTTP error", status: http.StatusBadRequest},
		{name: "read error", status: http.StatusOK, brokenBody: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := config.Defaults()
				cfg.HTTPClient.Retry.MaxAttempts = 1
				calls := 0
				base := &recorder{handle: func(*http.Request) (*http.Response, error) {
					calls++
					if calls > 1 {
						return response(200, nil, `[]`), nil
					}

					res := response(tt.status, http.Header{"X-Mbx-Used-Weight-1m": {"100"}}, `{}`)
					if tt.brokenBody {
						res.Body = brokenResponseBody{}
					}

					return res, nil
				}}
				c, transport := setup(t, cfg, BinanceSpot, base)
				_, err := send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/price")
				require.ErrorIs(t, err, application.ErrUpstream)
				ctx := begin(t, c, BinanceSpot, Tickers)

				_, err = send(ctx, transport, "/api/v3/ticker/price")

				assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
				assert.Len(t, base.sent(), 1)
				assert.Zero(t, c.Attempts(ctx))
			})
		})
	}
}

func TestRateSignalsAndWaitMetadata(t *testing.T) {
	cases := []struct {
		name     string
		scope    Scope
		status   int
		body     string
		header   string
		value    string
		duration time.Duration
	}{
		{"binance 429 fallback", BinanceSpot, 429, "", "", "", time.Minute},
		{"binance 418 fallback", BinanceLinear, 418, "", "", "", 72 * time.Hour},
		{"valid explicit ban wait", BinanceLinear, 418, "", "Retry-After", "3", 3 * time.Second},
		{"invalid negative wait", BinanceSpot, 429, "", "Retry-After", "-1", time.Minute},
		{"overflowing wait", BinanceSpot, 429, "", "Retry-After", "9223372036854775807", time.Minute},
		{"zero wait", BinanceSpot, 429, "", "Retry-After", "0", time.Minute},
		{"invalid reset", Bybit, 200, `{"retCode":10006}`, "X-Bapi-Limit-Reset-Timestamp", "invalid", time.Minute},
		{"missing reset", Bybit, 200, `{"retCode":10006}`, "", "", time.Minute},
		{"bybit 429", Bybit, 429, "", "", "", time.Minute},
		{"past reset", Bybit, 200, `{"retCode":10006}`, "X-Bapi-Limit-Reset-Timestamp", "1", time.Minute},
		{"bybit ban minimum", Bybit, 403, `{"retMsg":"Access too frequent. Please retry later."}`, "Retry-After", "1", 10 * time.Minute},
		{"bybit ban explicit longer", Bybit, 403, "access too frequently", "Retry-After", "900", 15 * time.Minute},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := config.Defaults()
				cfg.HTTPClient.Retry.MaxAttempts = 1
				headers := make(http.Header)
				if tt.header != "" {
					headers.Set(tt.header, tt.value)
				}
				recorder := &recorder{handle: func(*http.Request) (*http.Response, error) { return response(tt.status, headers, tt.body), nil }}
				c, transport := setup(t, cfg, tt.scope, recorder)
				path := "/api/v3/ticker/price"
				if tt.scope == BinanceLinear {
					path = "/fapi/v2/ticker/price"
				}
				if tt.scope == Bybit {
					path = "/v5/market/tickers?category=spot"
				}
				start := time.Now()
				_, err := send(begin(t, c, tt.scope, Tickers), transport, path)
				assert.ErrorIs(t, err, application.ErrUpstream)
				assert.Equal(t, start.Add(tt.duration), c.scopes[tt.scope].cooldown)
				if tt.duration >= time.Minute {
					_, err = send(begin(t, c, tt.scope, Tickers), transport, path)
					assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
					assert.Len(t, recorder.sent(), 1)
					assert.Equal(t, start, time.Now())
				}
			})
		})
	}
}

func TestRetryAfterAndResetOverrideBackoff(t *testing.T) {
	for _, signal := range []string{"seconds", "date", "reset", "server"} {
		t.Run(signal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				calls := 0
				recorder := &recorder{handle: func(*http.Request) (*http.Response, error) {
					calls++
					if calls > 1 {
						return response(200, nil, `{"retCode":0}`), nil
					}
					headers := make(http.Header)
					status := 429
					body := ""
					switch signal {
					case "seconds":
						headers.Set("Retry-After", "2")
					case "date":
						headers.Set("Retry-After", start.Add(2*time.Second).UTC().Format(http.TimeFormat))
					case "reset":
						status = 200
						body = `{"retCode":10006}`
						headers.Set("X-Bapi-Limit-Reset-Timestamp", strconv.FormatInt(start.Add(2*time.Second).UnixMilli(), 10))
					case "server":
						status = 503
						headers.Set("Retry-After", "2")
					}
					return response(status, headers, body), nil
				}}
				c, transport := setup(t, config.Defaults(), Bybit, recorder)
				_, err := send(begin(t, c, Bybit, Tickers), transport, "/v5/market/tickers?category=spot")
				require.NoError(t, err)
				sent := recorder.sent()
				require.Len(t, sent, 2)
				assert.Equal(t, 2*time.Second, sent[1].at.Sub(sent[0].at))
			})
		})
	}
}

func TestCooldownIsolationAndLateResponses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		recorder := &recorder{}
		c, transport := setup(t, cfg, Bybit, recorder)
		start := time.Now()
		c.Cooldown(Bybit, start.Add(20*time.Minute))
		c.Cooldown(Bybit, start.Add(time.Minute))
		_, err := send(begin(t, c, Bybit, Tickers), transport, "/v5/market/tickers?category=spot")
		assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
		assert.Equal(t, start.Add(20*time.Minute), c.scopes[Bybit].cooldown)
		spot, err := NewTransport(c, BinanceSpot, recorder, cfg, func(d time.Duration) time.Duration { return d }, nil)
		require.NoError(t, err)
		_, err = send(begin(t, c, BinanceSpot, Tickers), spot, "/api/v3/ticker/price")
		require.NoError(t, err)
		assert.Len(t, recorder.sent(), 1)
	})
}

func TestSuccessfulBybitHeadersNeverResetUsage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &recorder{handle: func(*http.Request) (*http.Response, error) {
			return response(200, http.Header{"X-Bapi-Limit": {"1"}, "X-Bapi-Limit-Status": {"1"}, "X-Bapi-Limit-Reset-Timestamp": {strconv.FormatInt(time.Now().UnixMilli(), 10)}}, `{"retCode":0}`), nil
		}}
		c, transport := setup(t, smallBybitConfig(), Bybit, recorder)
		start := time.Now()
		for range 14 {
			_, err := send(begin(t, c, Bybit, Tickers), transport, "/v5/market/tickers?category=spot")
			require.NoError(t, err)
		}
		assert.Equal(t, start.Add(5*time.Second), recorder.sent()[13].at)
	})
}

func TestUsageHeadersOnlyTightenTrustedEndpoints(t *testing.T) {
	for _, tt := range []struct {
		path    string
		tighten bool
	}{{"/fapi/v2/ticker/price", false}, {"/fapi/v1/ticker/bookTicker", false}, {"/fapi/v1/premiumIndex", true}} {
		t.Run(tt.path, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := config.Defaults()
				recorder := &recorder{handle: func(*http.Request) (*http.Response, error) {
					return response(200, http.Header{"X-Mbx-Used-Weight-1m": {"100"}}, `[]`), nil
				}}
				c, transport := setup(t, cfg, BinanceLinear, recorder)
				_, err := send(begin(t, c, BinanceLinear, Tickers), transport, tt.path)
				require.NoError(t, err)
				ctx := begin(t, c, BinanceLinear, Tickers)
				_, err = send(ctx, transport, tt.path)
				if tt.tighten {
					assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
					assert.Len(t, recorder.sent(), 1)
				} else {
					require.NoError(t, err)
					assert.Len(t, recorder.sent(), 2)
				}
			})
		})
	}
}

func TestSpotCatalogResponseUpdatesLimitsAndChargesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := `{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":2000},{"rateLimitType":"RAW_REQUESTS","interval":"MINUTE","intervalNum":5,"limit":300000},{"rateLimitType":"ORDERS","interval":"SECOND","intervalNum":10,"limit":100}],"symbols":[{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","status":"TRADING","filters":[{"filterType":"PRICE_FILTER","tickSize":"0.01"},{"filterType":"LOT_SIZE","stepSize":"0.00001"}]}]}`
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, nil, body), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		ctx := begin(t, c, BinanceSpot, Instruments)

		got, err := send(ctx, transport, "/api/v3/exchangeInfo?showPermissionSets=false")

		require.NoError(t, err)
		assert.Equal(t, body, string(got))
		assert.Len(t, base.sent(), 1)
		assert.Equal(t, 1, c.Attempts(ctx))
		state := c.scopes[BinanceSpot]
		assert.Equal(t, 1800, state.windows["request_weight_1m"].limit)
		require.Len(t, state.history, 1)
		assert.Equal(t, 20, state.history[0].cost.weight)
		assert.Equal(t, Instruments, state.history[0].cost.operation)
	})
}

func TestCatalogUpdatesKeepUsageAndRejectStaleResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &recorder{}
		c, transport := setup(t, config.Defaults(), BinanceSpot, recorder)
		start := time.Now()
		for range 3 {
			_, err := send(begin(t, c, BinanceSpot, MarketStats), transport, "/api/v3/ticker/24hr")
			require.NoError(t, err)
		}
		lower := []byte(`{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":2000}]}`)
		require.NoError(t, c.updateCatalog(BinanceSpot, start.Add(time.Second), lower))
		// A stale increase and a stale malformed catalog cannot replace newer limits.
		require.NoError(t, c.updateCatalog(BinanceSpot, start, []byte(`{"rateLimits":[]}`)))
		require.NoError(t, c.updateCatalog(BinanceSpot, start, []byte(`invalid`)))
		assert.Equal(t, 1800, c.scopes[BinanceSpot].windows["request_weight_1m"].limit)
		require.NoError(t, c.updateCatalog(BinanceSpot, start.Add(2*time.Second), []byte(`{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":999999}]}`)))
		assert.Equal(t, 899999, c.scopes[BinanceSpot].windows["request_weight_1m"].limit)
		assert.Len(t, c.scopes[BinanceSpot].history, 3)
		ctx, cancel := context.WithTimeout(begin(t, c, BinanceSpot, MarketStats), time.Second)
		defer cancel()
		_, err := send(ctx, transport, "/api/v3/ticker/24hr")
		require.NoError(t, err)
		assert.Len(t, recorder.sent(), 4)
	})
}

func TestMalformedCatalogKeepsBootstrapAndCanRecover(t *testing.T) {
	for _, body := range []string{`{}`, `{"rateLimits":null}`, `{"rateLimits":[{"rateLimitType":"UNKNOWN"}]}`, `{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":0}]}`, `{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":0,"limit":6000}]}`, `{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"DAY","intervalNum":9223372036854775807,"limit":6000}]}`} {
		t.Run(body, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recorder := &recorder{}
				c, transport := setup(t, config.Defaults(), BinanceSpot, recorder)
				start := time.Now()
				err := c.updateCatalog(BinanceSpot, start, []byte(body))
				assert.ErrorIs(t, err, application.ErrUpstreamUnavailable)
				_, err = send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/price")
				require.NoError(t, err)
				assert.Len(t, recorder.sent(), 1)
				require.NoError(t, c.updateCatalog(BinanceSpot, start.Add(time.Second), []byte(`{"rateLimits":[{"rateLimitType":"ORDERS"}]}`)))
				_, err = send(begin(t, c, BinanceSpot, Tickers), transport, "/api/v3/ticker/price")
				require.NoError(t, err)
			})
		})
	}
}

func TestNewLongWindowKeepsAvailableHistoryWithoutPause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &recorder{}
		c, transport := setup(t, config.Defaults(), BinanceLinear, recorder)
		start := time.Now()
		catalog := []byte(`{"rateLimits":[{"rateLimitType":"RAW_REQUESTS","interval":"HOUR","intervalNum":1,"limit":10000}]}`)
		require.NoError(t, c.updateCatalog(BinanceLinear, start, catalog))
		assert.True(t, c.scopes[BinanceLinear].cooldown.IsZero())
		assert.Contains(t, c.scopes[BinanceLinear].windows, "funding_requests_5m")
		assert.Contains(t, c.scopes[BinanceLinear].windows, "request_weight_1m")
		_, err := send(begin(t, c, BinanceLinear, Instruments), transport, "/fapi/v1/fundingInfo")
		require.NoError(t, err)
		require.NoError(t, err)
		assert.Len(t, recorder.sent(), 1)
	})
}

func TestRestartUsesBootstrapAndEmptyLocalState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		ceiling := cfg.Upstream.Limits.BinanceSpot.Windows["request_weight_1m"]
		ceiling.Limit = 12000
		cfg.Upstream.Limits.BinanceSpot.Windows["request_weight_1m"] = ceiling
		recorder := &recorder{}
		old, _ := setup(t, cfg, BinanceSpot, recorder)
		old.Cooldown(BinanceSpot, time.Now().Add(72*time.Hour))
		require.NoError(t, old.updateCatalog(BinanceSpot, time.Now(), []byte(`{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":2000}]}`)))
		c, transport := setup(t, cfg, BinanceSpot, recorder)
		start := time.Now()
		_, err := send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")
		require.NoError(t, err)
		assert.Equal(t, 5400, c.scopes[BinanceSpot].windows["request_weight_1m"].limit)
		assert.Equal(t, start, recorder.sent()[0].at)
	})
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	for _, seconds := range []int{1, 60, 3600} {
		until, ok := retryAfter(fmt.Sprint(seconds), now)
		assert.True(t, ok)
		assert.Equal(t, now.Add(time.Duration(seconds)*time.Second), until)
	}
	for _, invalid := range []string{"", "bad", "-1", "1.5", now.Add(-time.Second).Format(http.TimeFormat)} {
		_, ok := retryAfter(invalid, now)
		assert.False(t, ok)
	}
}
