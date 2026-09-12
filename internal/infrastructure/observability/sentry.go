package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/getsentry/sentry-go"
)

// Sentry owns a private client, so disabled instances never use a global hub.
// A nil receiver is the disabled adapter.
type Sentry struct{ client *sentry.Client }

func NewSentry(cfg config.Sentry) (*Sentry, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	transport := sentry.NewHTTPTransport()
	transport.Timeout = 2 * time.Second
	transport.BufferSize = 64
	return newSentry(cfg, transport)
}

func newSentry(cfg config.Sentry, transport sentry.Transport) (*Sentry, error) {
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn: cfg.DSN, Environment: cfg.Environment, EnableTracing: true,
		TracesSampleRate: cfg.TracesSampleRate, Transport: transport,
		MaxBreadcrumbs: -1, MaxSpans: 100,
		// No automatic request, environment, or source context collection.
		Integrations: func([]sentry.Integration) []sentry.Integration { return nil },
	})
	if err != nil {
		return nil, errors.New("initialize Sentry: invalid client settings")
	}
	return &Sentry{client: client}, nil
}

// ErrorCode deliberately excludes raw upstream, storage, and panic text.
func ErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	var known *application.Error
	if errors.As(err, &known) {
		return known.Code()
	}
	return "internal_error"
}

func (s *Sentry) Report(err error, tags map[string]string) {
	if s == nil || err == nil || errors.Is(err, context.Canceled) {
		return
	}
	s.reportCode(ErrorCode(err), tags)
}

func (s *Sentry) reportCode(code string, tags map[string]string) {
	if s == nil {
		return
	}
	event := sentry.NewEvent()
	event.Level = sentry.LevelError
	event.Message = code
	event.Tags = tags
	s.client.CaptureEvent(event, nil, sentry.NewScope())
}

func (s *Sentry) Panic(tags map[string]string) {
	if s == nil {
		return
	}
	event := sentry.NewEvent()
	event.Level = sentry.LevelFatal
	event.Message = "Operation panicked"
	event.Tags = tags
	event.Exception = []sentry.Exception{{Type: "panic", Value: "Operation panicked", Stacktrace: sentry.NewStacktrace()}}
	s.client.CaptureEvent(event, nil, sentry.NewScope())
}

func (s *Sentry) Trace(ctx context.Context, operation string, tags map[string]string) func() {
	if s == nil {
		return func() {}
	}
	hub := sentry.NewHub(s.client, sentry.NewScope())
	span := sentry.StartTransaction(sentry.SetHubOnContext(ctx, hub), operation, sentry.WithOpName(operation))
	for key, value := range tags {
		span.SetTag(key, value)
	}
	return span.Finish
}

func (s *Sentry) ObserveExchange(event upstream.Event) {
	if s == nil {
		return
	}
	scope := exchangeScope(event)
	tags := scopeLabels(scope.Scope)
	tags["operation"] = scope.Operation
	s.Report(event.Error, tags)
	hub := sentry.NewHub(s.client, sentry.NewScope())
	span := sentry.StartTransaction(sentry.SetHubOnContext(context.Background(), hub), "exchange."+operationName(event.Operation), sentry.WithOpName("http.client"))
	span.StartTime = event.StartedAt
	span.EndTime = event.StartedAt.Add(event.Duration)
	for key, value := range tags {
		span.SetTag(key, value)
	}
	span.SetTag("status", fmt.Sprint(event.Status))
	span.Finish()
}

func (s *Sentry) Flush(ctx context.Context) bool {
	if s == nil {
		return true
	}
	return s.client.FlushWithContext(ctx)
}

func (s *Sentry) Close() {
	if s != nil {
		s.client.Close()
	}
}

var safeSymbol = regexp.MustCompile(`^[A-Z0-9_-]{1,64}$`)

func requestTags(r *http.Request) map[string]string {
	tags := map[string]string{"operation": "http.server"}
	query := r.URL.Query()
	if exchange := domain.Exchange(query.Get("exchange")); exchange.Valid() {
		tags["exchange"] = string(exchange)
	}
	if market := domain.Market(query.Get("market")); market.Valid() {
		tags["market"] = string(market)
	}
	if interval := domain.Timeframe(query.Get("interval")); interval.Valid() {
		tags["interval"] = string(interval)
	}
	if symbol := query.Get("symbol"); safeSymbol.MatchString(symbol) {
		tags["symbol"] = symbol
	}
	return tags
}

// HTTP reports fixed route names and safe context. Headers, bodies, raw queries,
// unknown paths, and panic values never enter telemetry or logs.
func (s *Sentry) HTTP(next http.Handler, routes map[string]http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path
		if routes[name] == nil && name != "/health" && name != "/ready" {
			name = "unknown"
		}
		tags := requestTags(r)
		tags["route"] = name
		finish := s.Trace(r.Context(), "HTTP "+name, tags)
		defer finish()
		response := &statusWriter{ResponseWriter: w}
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				s.Panic(tags)
				logger.Error("HTTP handler panicked", "operation", name)
				if response.status != 0 {
					panic(http.ErrAbortHandler)
				}
				response.Header().Set("Content-Type", "application/json")
				response.WriteHeader(http.StatusInternalServerError)
				_, _ = response.Write([]byte(`{"error":{"code":"internal_error","message":"Internal error"}}`))
			}
			canceled := errors.Is(response.cause, context.Canceled) ||
				(response.cause == nil && errors.Is(r.Context().Err(), context.Canceled))
			if response.status >= 500 && response.failureCode != application.ErrDataNotReady.Code() && !canceled {
				code := response.failureCode
				if code == "" {
					code = "http_error"
					if response.status == http.StatusInternalServerError {
						code = application.ErrInternal.Code()
					}
				}
				errorTags := maps.Clone(tags)
				errorTags["status"] = fmt.Sprint(response.status)
				s.reportCode(code, errorTags)
			}
			logger.Debug("HTTP request completed", "operation", name, "status", response.status, "duration", time.Since(started))
		}()
		next.ServeHTTP(response, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	failureCode string
	cause       error
}

func (w *statusWriter) ObserveHTTPError(code string, cause error) {
	w.failureCode = code
	w.cause = cause
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
