package upstream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"market-data/internal/application"
	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionLossRetriesPassAdmission(t *testing.T) {
	cases := []struct {
		name        string
		maxAttempts int
		wantError   error
	}{
		{name: "one attempt", maxAttempts: 1, wantError: application.ErrUpstream},
		{name: "retry allowed", maxAttempts: 2},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Empty(t, body)
				assert.Empty(t, r.TransferEncoding)
				assert.Equal(t, http.MethodGet, r.Method)

				// The second GET has reached the server before the connection fails.
				if calls.Add(1) == 2 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if !assert.NoError(t, err) {
						return
					}
					assert.NoError(t, conn.Close())
					return
				}

				_, err = io.WriteString(w, `{"retCode":0,"result":{"price":"12.34"}}`)
				assert.NoError(t, err)
			}))
			defer server.Close()

			cfg := config.Defaults()
			cfg.HTTPClient.Retry.MaxAttempts = tt.maxAttempts
			cfg.Upstream.LanesPerExchange.Tickers.MaxAttempts = tt.maxAttempts
			base := http.DefaultTransport.(*http.Transport).Clone()
			defer base.CloseIdleConnections()
			c, transport := setup(t, cfg, Bybit, base)
			fetch := func(ctx context.Context) ([]byte, error) {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v5/market/tickers?category=spot", nil)
				require.NoError(t, err)

				res, err := transport.RoundTrip(req)
				if err != nil {
					return nil, err
				}
				defer func() { assert.NoError(t, res.Body.Close()) }()

				return io.ReadAll(res.Body)
			}

			// Warm up a reusable connection: net/http retries failed GETs on it.
			_, err := fetch(begin(t, c, Bybit, Tickers))
			require.NoError(t, err)
			ctx := begin(t, c, Bybit, Tickers)

			body, err := fetch(ctx)

			if tt.wantError != nil {
				assert.ErrorIs(t, err, tt.wantError)
				assert.Empty(t, body)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, `{"retCode":0,"result":{"price":"12.34"}}`, string(body))
			}
			assert.Equal(t, int64(1+tt.maxAttempts), calls.Load())
			assert.Equal(t, tt.maxAttempts, c.Attempts(ctx))
		})
	}
}

func TestAttemptPreservesEmptyGETAcrossHTTPProtocols(t *testing.T) {
	cases := []struct {
		name      string
		http2     bool
		wantProto int
	}{
		{name: "HTTP 1.1", wantProto: 1},
		{name: "HTTP 2", http2: true, wantProto: 2},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Empty(t, body)
				assert.Empty(t, r.TransferEncoding)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, tt.wantProto, r.ProtoMajor)

				_, err = io.WriteString(w, `{"retCode":0}`)
				assert.NoError(t, err)
			}))
			server.EnableHTTP2 = tt.http2
			server.StartTLS()
			defer server.Close()

			c, transport := setup(t, config.Defaults(), Bybit, server.Client().Transport)
			ctx := begin(t, c, Bybit, Tickers)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v5/market/tickers?category=spot", nil)
			require.NoError(t, err)

			res, err := transport.RoundTrip(req)

			require.NoError(t, err)
			defer func() { assert.NoError(t, res.Body.Close()) }()
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			assert.Equal(t, `{"retCode":0}`, string(body))
			assert.Nil(t, req.Body)
			assert.Equal(t, 1, c.Attempts(ctx))
		})
	}
}
