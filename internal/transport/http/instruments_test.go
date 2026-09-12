package httptransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/instrument"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/storage/memory"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func instrumentHandler(reader InstrumentReader, timeout time.Duration, maximum int) http.Handler {
	return NewAPIHandler(func() bool { return true }, 8192, map[string]http.Handler{"/api/v1/instruments": NewInstrumentsHandler(reader, timeout, maximum)})
}

func TestInstrumentsHTTPContract(t *testing.T) {
	cases := []struct {
		name, method, query, body string
		status                    int
		code                      string
	}{
		{"empty snapshot", "GET", "", "", 200, ""},
		{"absent symbol", "GET", "symbol=ABSENT", "", 200, ""},
		{"unknown key", "GET", "extra=x", "", 400, "invalid_parameter"},
		{"malformed encoding", "GET", "symbol=%zz", "", 400, "invalid_parameter"},
		{"semicolon", "GET", "symbol=A;B", "", 400, "invalid_parameter"},
		{"repeated exchange", "GET", "exchange=binance&exchange=bybit", "", 400, "invalid_parameter"},
		{"empty exchange", "GET", "exchange=", "", 400, "invalid_parameter"},
		{"empty symbol", "GET", "symbol=", "", 400, "invalid_parameter"},
		{"empty status", "GET", "status=", "", 400, "invalid_status"},
		{"repeated status", "GET", "status=trading&status=closed", "", 400, "invalid_status"},
		{"unknown status", "GET", "status=Trading", "", 400, "invalid_status"},
		{"unknown exchange", "GET", "exchange=other", "", 400, "invalid_filter"},
		{"disabled exchange", "GET", "exchange=bybit", "", 400, "invalid_filter"},
		{"disabled market", "GET", "market=linear", "", 400, "invalid_filter"},
		{"symbol whitespace", "GET", "symbol=A+B", "", 400, "invalid_filter"},
		{"invalid UTF8", "GET", "symbol=%ff", "", 400, "invalid_filter"},
		{"wrong method", "POST", "", "", 405, "method_not_allowed"},
		{"body", "GET", "", "x", 400, "invalid_parameter"},
		{"large query", "GET", "symbol=" + strings.Repeat("a", 8192), "", 414, "request_too_large"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			repo := memory.NewInstrumentRepository()
			scope := application.Scope{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot}
			require.NoError(t, repo.ReplaceSnapshot(t.Context(), scope, nil))
			handler := instrumentHandler(instrument.NewReader(repo, []application.Scope{scope}), time.Second, 1)
			request := httptest.NewRequestWithContext(t.Context(), tt.method, "/api/v1/instruments?"+tt.query, strings.NewReader(tt.body))
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assert.Equal(t, tt.status, response.Code)
			assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
			if tt.code == "" {
				assert.JSONEq(t, `{"data":[]}`, response.Body.String())
			} else {
				assert.Contains(t, response.Body.String(), `"code":"`+tt.code+`"`)
			}
		})
	}
}

func TestInstrumentDTOUsesStringsNullsAndUTC(t *testing.T) {
	repo := memory.NewInstrumentRepository()
	scope := application.Scope{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear}
	now := time.Date(2026, 9, 12, 12, 0, 0, 123, time.FixedZone("offset", 7200))
	interval := 8 * time.Hour
	minimum := decimal.RequireFromString("0.000000000000000123")
	rows := []domain.Instrument{{Exchange: scope.Exchange, Market: scope.Market, Symbol: "ABCUSDT", BaseAsset: "ABC", QuoteAsset: "USDT", Status: domain.InstrumentStatusTrading, PriceTick: minimum, QtyStep: decimal.RequireFromString("0.000001"), MinQty: &minimum, FundingInterval: &interval, DelistingTime: &now, UpdatedAt: now}}
	require.NoError(t, repo.ReplaceSnapshot(t.Context(), scope, rows))
	handler := instrumentHandler(instrument.NewReader(repo, []application.Scope{scope}), time.Second, 1)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/instruments", nil))

	assert.Equal(t, 200, response.Code)
	assert.JSONEq(t, `{"data":[{"exchange":"bybit","market":"linear","symbol":"ABCUSDT","base_asset":"ABC","quote_asset":"USDT","status":"trading","price_tick":"0.000000000000000123","qty_step":"0.000001","min_qty":"0.000000000000000123","max_qty":null,"min_notional":null,"funding_interval":28800,"delisting_time":"2026-09-12T10:00:00.000000123Z","updated_at":"2026-09-12T10:00:00.000000123Z"}]}`, response.Body.String())
}

type readerFunc func(context.Context, instrument.Query) ([]domain.Instrument, error)

func (readerFunc) Validate(instrument.Query) error { return nil }

func (f readerFunc) List(ctx context.Context, q instrument.Query) ([]domain.Instrument, error) {
	return f(ctx, q)
}

func TestInstrumentErrorsDoNotExposeInternalDetails(t *testing.T) {
	cases := []struct {
		name    string
		failure error
		status  int
		code    string
	}{
		{"repository", errors.New("private database details"), 500, "internal_error"},
		{"unready", application.ErrDataNotReady, 503, "data_not_ready"},
		{"deadline", context.DeadlineExceeded, 504, "request_timeout"},
		{"canceled", context.Canceled, 504, "request_timeout"},
		{"overloaded", application.ErrServiceOverloaded, 503, "service_overloaded"},
		{"upstream", application.ErrUpstream, 502, "upstream_error"},
		{"unsupported internal operation", application.ErrUnsupportedOperation, 500, "internal_error"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			handler := instrumentHandler(readerFunc(func(context.Context, instrument.Query) ([]domain.Instrument, error) { return nil, tt.failure }), time.Second, 1)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/instruments", nil))

			assert.Equal(t, tt.status, response.Code)
			assert.Contains(t, response.Body.String(), `"code":"`+tt.code+`"`)
			assert.NotContains(t, response.Body.String(), "private")
		})
	}
}

func TestSnapshotAdmissionAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		handler := instrumentHandler(readerFunc(func(ctx context.Context, _ instrument.Query) ([]domain.Instrument, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}), 5*time.Second, 1)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		first := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			handler.ServeHTTP(first, httptest.NewRequestWithContext(ctx, "GET", "/api/v1/instruments", nil))
			close(done)
		}()
		synctest.Wait()

		excess := httptest.NewRecorder()
		handler.ServeHTTP(excess, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/instruments", nil))

		assert.Equal(t, 503, excess.Code)
		assert.Contains(t, excess.Body.String(), "service_overloaded")
		health := httptest.NewRecorder()
		handler.ServeHTTP(health, httptest.NewRequestWithContext(t.Context(), "GET", "/ready", nil))
		assert.Equal(t, 200, health.Code)
		cancel()
		<-done
		next := httptest.NewRecorder()
		handler.ServeHTTP(next, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/instruments", nil))
		assert.Equal(t, 504, next.Code)
		assert.Contains(t, next.Body.String(), "request_timeout")
	})
}

func TestSerializationFailureDoesNotWritePartialData(t *testing.T) {
	rows := []domain.Instrument{{UpdatedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}}
	handler := instrumentHandler(readerFunc(func(context.Context, instrument.Query) ([]domain.Instrument, error) { return rows, nil }), time.Second, 1)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/instruments", nil))

	assert.Equal(t, 500, response.Code)
	assert.JSONEq(t, `{"error":{"code":"internal_error","message":"Internal error"}}`, response.Body.String())
}

type blockedInstrumentRepository struct {
	instrument.Repository
}

func (r blockedInstrumentRepository) List(ctx context.Context, _ instrument.Filter) ([]domain.Instrument, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestFilterValidationPrecedesSnapshotAdmission(t *testing.T) {
	cases := []struct {
		name, query string
		status      int
		code        string
	}{
		{"unknown exchange", "exchange=invalid", 400, "invalid_filter"},
		{"unknown market", "market=invalid", 400, "invalid_filter"},
		{"disabled pair", "exchange=bybit&market=spot", 400, "invalid_filter"},
		{"invalid symbol", "symbol=A+B", 400, "invalid_filter"},
		{"invalid UTF8", "symbol=%ff", 400, "invalid_filter"},
		{"unknown status", "status=invalid", 400, "invalid_status"},
		{"filter before status", "exchange=invalid&status=invalid", 400, "invalid_filter"},
		{"valid request still overloaded", "exchange=binance&market=spot&status=trading", 503, "service_overloaded"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				repo := blockedInstrumentRepository{Repository: memory.NewInstrumentRepository()}
				reader := instrument.NewReader(repo, []application.Scope{
					{Exchange: domain.ExchangeBinance, Market: domain.MarketSpot},
					{Exchange: domain.ExchangeBybit, Market: domain.MarketLinear},
				})
				handler := instrumentHandler(reader, 5*time.Second, 1)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				first := httptest.NewRecorder()
				done := make(chan struct{})
				go func() {
					defer close(done)
					handler.ServeHTTP(first, httptest.NewRequestWithContext(ctx, "GET", "/api/v1/instruments", nil))
				}()
				synctest.Wait()
				response := httptest.NewRecorder()

				handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/instruments?"+tt.query, nil))

				assert.Equal(t, tt.status, response.Code)
				assert.Contains(t, response.Body.String(), `"code":"`+tt.code+`"`)
				cancel()
				<-done
				assert.Equal(t, 504, first.Code)
			})
		})
	}
}
