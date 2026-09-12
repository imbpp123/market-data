package httptransport

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type klineReaderStub struct {
	validate func(kline.Series) error
	get      func(context.Context, kline.Query) ([]domain.Kline, error)
}

func (r klineReaderStub) ValidateSeries(series kline.Series) error {
	if r.validate != nil {
		return r.validate(series)
	}
	return nil
}

func (r klineReaderStub) Get(ctx context.Context, query kline.Query) ([]domain.Kline, error) {
	return r.get(ctx, query)
}

func (klineReaderStub) Validate(kline.Query) error { return nil }

func klineParameters() url.Values {
	return url.Values{"exchange": {"binance"}, "market": {"spot"}, "symbol": {"BTCUSDT"}, "interval": {"1m"}, "from": {"2026-09-12T12:00:00Z"}, "to": {"2026-09-12T12:01:00Z"}}
}

func TestKlineHTTPRejectsQueryShape(t *testing.T) {
	cases := []struct {
		name, key string
		values    []string
		code      string
	}{
		{"missing exchange", "exchange", nil, "invalid_parameter"},
		{"missing market", "market", nil, "invalid_parameter"},
		{"missing symbol", "symbol", nil, "invalid_parameter"},
		{"missing interval", "interval", nil, "invalid_parameter"},
		{"missing start", "from", nil, "invalid_parameter"},
		{"missing end", "to", nil, "invalid_parameter"},
		{"empty exchange", "exchange", []string{""}, "invalid_parameter"},
		{"repeated symbol", "symbol", []string{"A", "B"}, "invalid_parameter"},
		{"empty interval", "interval", []string{""}, "invalid_interval"},
		{"repeated interval", "interval", []string{"1m", "1m"}, "invalid_interval"},
		{"unknown parameter", "limit", []string{"1"}, "invalid_parameter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parameters := klineParameters()
			if tc.values == nil {
				delete(parameters, tc.key)
			} else {
				parameters[tc.key] = tc.values
			}
			reader := klineReaderStub{get: func(context.Context, kline.Query) ([]domain.Kline, error) {
				t.Error("invalid query reached data access")
				return nil, nil
			}}
			response := httptest.NewRecorder()

			NewKlinesHandler(reader, time.Second, 64).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/klines?"+parameters.Encode(), nil))

			assert.Equal(t, 400, response.Code)
			assert.Contains(t, response.Body.String(), `"code":"`+tc.code+`"`)
		})
	}
}

func TestKlineHTTPTimestamps(t *testing.T) {
	cases := []struct {
		name, value string
		valid       bool
	}{
		{"UTC", "2026-09-12T12:00:00Z", true},
		{"offset", "2026-09-12T14:00:00+02:00", true},
		{"nanoseconds", "2026-09-12T12:00:00.123456789Z", true},
		{"Unix integer", "1789214400", false},
		{"date only", "2026-09-12", false},
		{"missing zone", "2026-09-12T12:00:00", false},
		{"leap second", "2026-09-12T12:00:60Z", false},
		{"too many digits", "2026-09-12T12:00:00.1234567890Z", false},
		{"bad date", "2026-02-30T12:00:00Z", false},
		{"before epoch", "1969-12-31T23:59:59Z", false},
		{"comma fraction", "2026-09-12T12:00:00,1Z", false},
		{"bad offset hour", "2026-09-12T12:00:00+24:00", false},
		{"bad offset minute", "2026-09-12T12:00:00+01:60", false},
		{"single digit hour", "2026-09-12T2:00:00Z", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parameters := klineParameters()
			parameters.Set("from", tc.value)
			reader := klineReaderStub{get: func(_ context.Context, query kline.Query) ([]domain.Kline, error) {
				assert.True(t, tc.valid, "malformed timestamp reached data access")
				assert.Equal(t, time.UTC, query.From.Location())
				return nil, nil
			}}
			response := httptest.NewRecorder()

			NewKlinesHandler(reader, time.Second, 64).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/klines?"+parameters.Encode(), nil))

			if tc.valid {
				assert.Equal(t, 200, response.Code)
				assert.JSONEq(t, `{"data":[]}`, response.Body.String())
			} else {
				assert.Equal(t, 400, response.Code)
				assert.Contains(t, response.Body.String(), "invalid_range")
			}
		})
	}
}

func TestKlineHTTPFilterValidationPrecedesTimestamps(t *testing.T) {
	reader := klineReaderStub{validate: func(kline.Series) error { return application.ErrInvalidFilter }, get: func(context.Context, kline.Query) ([]domain.Kline, error) {
		t.Error("invalid filter reached data access")
		return nil, nil
	}}
	parameters := klineParameters()
	parameters.Set("from", "invalid")
	response := httptest.NewRecorder()

	NewKlinesHandler(reader, time.Second, 64).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/klines?"+parameters.Encode(), nil))

	assert.Equal(t, 400, response.Code)
	assert.Contains(t, response.Body.String(), "invalid_filter")
}

func TestKlineHTTPMalformedEncoding(t *testing.T) {
	response := httptest.NewRecorder()

	NewKlinesHandler(klineReaderStub{}, time.Second, 64).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/klines?from=%zz", nil))

	assert.Equal(t, 400, response.Code)
	assert.Contains(t, response.Body.String(), "invalid_parameter")
}

func TestKlineHTTPExactDTO(t *testing.T) {
	at := time.Date(2026, 9, 12, 14, 0, 0, 0, time.FixedZone("offset", 7200))
	reader := klineReaderStub{get: func(context.Context, kline.Query) ([]domain.Kline, error) {
		return []domain.Kline{{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot, Symbol: "BTCUSDT", Interval: domain.Timeframe1m,
			OpenTime: at, CloseTime: at.Add(time.Minute), FetchedAt: at.Add(2*time.Minute + 123*time.Nanosecond),
			Open: decimal.RequireFromString("12345678901234567890.123456789"), High: decimal.RequireFromString("12345678901234567890.123456789"), Low: decimal.NewFromInt(1), Close: decimal.NewFromInt(2), Volume: decimal.NewFromInt(3), Turnover: decimal.RequireFromString("4.1234567890123456789"),
		}}, nil
	}}
	response := httptest.NewRecorder()

	NewKlinesHandler(reader, time.Second, 64).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/klines?"+klineParameters().Encode(), nil))

	require.Equal(t, 200, response.Code)
	assert.JSONEq(t, `{"data":[{"exchange":"binance","market":"spot","symbol":"BTCUSDT","interval":"1m","open_time":"2026-09-12T12:00:00Z","close_time":"2026-09-12T12:01:00Z","open":"12345678901234567890.123456789","high":"12345678901234567890.123456789","low":"1","close":"2","volume":"3","turnover":"4.1234567890123456789","trades_count":null,"fetched_at":"2026-09-12T12:02:00.000000123Z"}]}`, response.Body.String())
}

func TestKlineHTTPErrorsHideInternalDetails(t *testing.T) {
	cases := []struct {
		failure error
		status  int
		code    string
	}{
		{application.ErrInvalidRange, 400, "invalid_range"},
		{application.ErrRequestTooLarge, 400, "request_too_large"},
		{application.ErrRangeOutOfRetention, 400, "range_out_of_retention"},
		{application.ErrDataNotReady, 503, "data_not_ready"},
		{application.ErrSymbolNotFound, 404, "symbol_not_found"},
		{application.ErrServiceOverloaded, 503, "service_overloaded"},
		{application.ErrUpstreamUnavailable, 503, "upstream_unavailable"},
		{application.ErrUpstreamAttemptLimit, 502, "upstream_attempt_limit"},
		{application.ErrIncompleteData, 502, "incomplete_data"},
		{application.ErrInvalidUpstreamData, 502, "invalid_upstream_data"},
		{application.ErrUpstream, 502, "upstream_error"},
		{context.DeadlineExceeded, 504, "request_timeout"},
		{errors.New("SDK response details"), 500, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			reader := klineReaderStub{get: func(context.Context, kline.Query) ([]domain.Kline, error) {
				return nil, errors.Join(tc.failure, errors.New("SDK response details"))
			}}
			response := httptest.NewRecorder()

			NewKlinesHandler(reader, time.Second, 64).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/klines?"+klineParameters().Encode(), nil))

			assert.Equal(t, tc.status, response.Code)
			assert.Contains(t, response.Body.String(), `"code":"`+tc.code+`"`)
			assert.NotContains(t, response.Body.String(), "SDK")
			assert.NotContains(t, response.Body.String(), `"data"`)
		})
	}
}

func TestKlineHTTPDeadlineStartsBeforeValidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader := klineReaderStub{validate: func(kline.Series) error {
			time.Sleep(500 * time.Millisecond)
			return nil
		}, get: func(ctx context.Context, _ kline.Query) ([]domain.Kline, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		response := httptest.NewRecorder()
		started := time.Now()

		NewKlinesHandler(reader, time.Second, 64).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/klines?"+klineParameters().Encode(), nil))

		assert.Equal(t, time.Second, time.Since(started))
		assert.Equal(t, 504, response.Code)
	})
}
