package upstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type sentRequest struct {
	at   time.Time
	path string
}

type recorder struct {
	mu       sync.Mutex
	requests []sentRequest
	handle   roundTripFunc
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.requests = append(r.requests, sentRequest{at: time.Now(), path: req.URL.RequestURI()})
	r.mu.Unlock()

	if r.handle != nil {
		return r.handle(req)
	}

	body := `{"retCode":0,"result":{"price":"1.234567890123456789"}}`
	if strings.Contains(req.URL.Path, "exchangeInfo") {
		body = `{"rateLimits":[]}`
	}

	return response(200, nil, body), nil
}

func (r *recorder) sent() []sentRequest {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]sentRequest(nil), r.requests...)
}

func response(status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}

	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func setup(t *testing.T, cfg config.Config, scope Scope, recorder http.RoundTripper) (*Controller, *Transport) {
	t.Helper()

	c, err := New(cfg, SystemClock{})
	require.NoError(t, err)

	transport, err := NewTransport(c, scope, recorder, cfg, func(d time.Duration) time.Duration { return d }, nil)
	require.NoError(t, err)

	return c, transport
}

func begin(t *testing.T, c *Controller, scope Scope, kind Operation) context.Context {
	t.Helper()

	ctx, cancel, err := c.Begin(t.Context(), scope, kind)
	require.NoError(t, err)
	t.Cleanup(cancel)

	return ctx
}

func send(ctx context.Context, transport *Transport, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://exchange.test"+path, nil)
	if err != nil {
		return nil, err
	}

	res, err := transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()

	return io.ReadAll(res.Body)
}

func TestRetriesChargeEveryAttemptAndPreserveData(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		calls := 0

		recorder := &recorder{handle: func(*http.Request) (*http.Response, error) {
			calls++

			if calls == 1 {
				return nil, io.ErrUnexpectedEOF
			}
			if calls == 2 {
				return response(503, nil, "unavailable"), nil
			}

			return response(200, nil, `{"retCode":0,"result":{"value":9007199254740993}}`), nil
		}}

		c, transport := setup(t, cfg, Bybit, recorder)
		ctx := begin(t, c, Bybit, Tickers)
		start := time.Now()

		data, err := send(ctx, transport, "/v5/market/tickers?category=spot")

		require.NoError(t, err)
		assert.Contains(t, string(data), "9007199254740993")
		assert.Equal(t, 3, c.Attempts(ctx))
		assert.Equal(t, 300*time.Millisecond, time.Since(start))

		sent := recorder.sent()
		require.Len(t, sent, 3)
		assert.Equal(t, start.Add(100*time.Millisecond), sent[1].at)
		assert.Equal(t, start.Add(300*time.Millisecond), sent[2].at)
	})
}

func TestTransportFailureClasses(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		failure  error
		attempts int
		expected error
	}{
		{"bad request", 400, "bad", nil, 1, application.ErrUpstream},
		{"unrelated forbidden", 403, "restricted location", nil, 1, application.ErrUpstream},
		{"server error", 500, "bad", nil, 3, application.ErrUpstream},
		{"bad gateway", 502, "bad", nil, 3, application.ErrUpstream},
		{"gateway timeout", 504, "bad", nil, 3, application.ErrUpstream},
		{"not implemented", 501, "bad", nil, 1, application.ErrUpstream},
		{"invalid envelope", 200, `{"result":{}}`, nil, 1, application.ErrInvalidUpstreamData},
		{"fractional code", 200, `{"retCode":0.1}`, nil, 1, application.ErrInvalidUpstreamData},
		{"exchange error", 200, `{"retCode":10001}`, nil, 1, application.ErrUpstream},
		{"permanent transport", 0, "", errors.New("invalid TLS settings"), 1, application.ErrUpstream},
		{"temporary transport", 0, "", io.ErrUnexpectedEOF, 3, application.ErrUpstream},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recorder := &recorder{handle: func(*http.Request) (*http.Response, error) {
					if tt.failure != nil {
						return nil, tt.failure
					}

					return response(tt.status, nil, tt.body), nil
				}}
				c, transport := setup(t, config.Defaults(), Bybit, recorder)

				_, err := send(begin(t, c, Bybit, Tickers), transport, "/v5/market/tickers?category=spot")

				assert.ErrorIs(t, err, tt.expected)
				assert.Len(t, recorder.sent(), tt.attempts)
			})
		})
	}
}

func TestPagesShareAttemptBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Upstream.LanesPerExchange.Instruments.MaxAttempts = 2

		recorder := &recorder{}
		c, transport := setup(t, cfg, Bybit, recorder)
		ctx := begin(t, c, Bybit, Instruments)

		for range 2 {
			_, err := send(ctx, transport, "/v5/market/instruments-info?category=linear")
			require.NoError(t, err)
		}

		_, err := send(ctx, transport, "/v5/market/instruments-info?category=linear&cursor=page3")

		assert.ErrorIs(t, err, application.ErrUpstreamAttemptLimit)
		assert.Len(t, recorder.sent(), 2)
		assert.Equal(t, 2, c.Attempts(ctx))
	})
}

func TestAttemptTimeoutDoesNotIncludeAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.HTTPClient.Timeout = 100 * time.Millisecond

		recorder := &recorder{handle: func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()

			return nil, r.Context().Err()
		}}

		c, transport := setup(t, cfg, Bybit, recorder)
		c.Cooldown(Bybit, time.Now().Add(time.Second))
		ctx := begin(t, c, Bybit, Tickers)
		start := time.Now()

		_, err := send(ctx, transport, "/v5/market/tickers?category=spot")

		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Len(t, recorder.sent(), 3)
		assert.Equal(t, 1600*time.Millisecond, time.Since(start))
	})
}

func TestCanceledAndInvalidRequestsSpendNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &recorder{}
		c, transport := setup(t, config.Defaults(), Bybit, recorder)
		ctx := begin(t, c, Bybit, Tickers)
		canceled, cancel := context.WithCancel(ctx)
		cancel()

		_, err := send(canceled, transport, "/v5/market/tickers?category=spot")
		assert.ErrorIs(t, err, context.Canceled)

		_, err = send(ctx, transport, "/v5/market/kline?category=spot&limit=1")
		assert.ErrorIs(t, err, application.ErrUnsupportedOperation)

		_, err = send(t.Context(), transport, "/v5/market/tickers?category=spot")
		assert.ErrorIs(t, err, application.ErrUnsupportedOperation)

		assert.Empty(t, recorder.sent())
		assert.Zero(t, c.Attempts(ctx))
	})
}

func TestResponseLimitAndRequestLocalEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.HTTPClient.MaxResponseBytes = 32

		recorder := &recorder{handle: func(r *http.Request) (*http.Response, error) {
			return response(200, http.Header{"X-Request": {r.URL.Query().Get("symbol")}}, strings.Repeat("x", 33)), nil
		}}
		c, transport := setup(t, cfg, Bybit, recorder)

		var events []Event
		transport.observe = func(e Event) { events = append(events, e) }
		ctx := begin(t, c, Bybit, Tickers)

		_, err := send(ctx, transport, "/v5/market/tickers?category=spot&symbol=BTCUSDT")

		assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
		assert.Equal(t, 1, c.Attempts(ctx))

		require.Len(t, events, 1)
		assert.Equal(t, 200, events[0].Status)
		assert.Equal(t, "BTCUSDT", events[0].Header.Get("X-Request"))
		assert.Error(t, events[0].Error)
	})
}
