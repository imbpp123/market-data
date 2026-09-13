package upstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/domain"
)

var errResponseTooLarge = fmt.Errorf("response body exceeds byte limit: %w", application.ErrInvalidUpstreamData)

// Event is immutable request-local metadata. Observers must be concurrency safe.
// Bodies and raw error strings are deliberately excluded from statistics events.
type Event struct {
	FailureReason string
	Scope         Scope
	Market        domain.Market
	Operation     Operation
	Path          string
	StartedAt     time.Time
	Duration      time.Duration
	Status        int
	Code          int64
	Header        http.Header
	Error         error
}

type Observer func(Event)

type Transport struct {
	controller *Controller
	scope      Scope
	base       http.RoundTripper
	settings   config.HTTPClient
	maxQuery   int
	clock      Clock
	jitter     Jitter
	observe    Observer
	pageLimits map[string]int
}

func NewTransport(controller *Controller, scope Scope, base http.RoundTripper, cfg config.Config, jitter Jitter, observe Observer) (*Transport, error) {
	if controller == nil || base == nil || jitter == nil {
		return nil, fmt.Errorf("upstream dependencies are required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	controller.mu.Lock()
	_, ok := controller.scopes[scope]
	controller.mu.Unlock()
	if !ok {
		return nil, application.ErrUnsupportedOperation
	}
	t := &Transport{controller: controller, scope: scope, base: base, settings: cfg.HTTPClient, maxQuery: cfg.Server.MaxQueryBytes, clock: controller.clock, jitter: jitter, observe: observe}
	t.pageLimits = make(map[string]int)
	if scope == Bybit {
		for _, market := range cfg.Exchanges.Bybit.Markets {
			maximum := cfg.Exchanges.Bybit.Klines.MaxCandlesPerRequest.Spot
			if market == "linear" {
				maximum = cfg.Exchanges.Bybit.Klines.MaxCandlesPerRequest.Linear
			}
			t.pageLimits[market] = maximum
		}
	} else {
		maximum := cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Spot
		if scope == BinanceLinear {
			maximum = cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Linear
		}
		t.pageLimits[""] = maximum
	}
	return t, nil
}

func parseQuery(request *http.Request) (url.Values, error) {
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return nil, application.ErrInvalidParameter
	}
	for _, v := range values {
		if len(v) != 1 || v[0] == "" {
			return nil, application.ErrInvalidParameter
		}
	}
	return values, nil
}

// HTTPClient prevents redirects from adding implicit SDK calls or changing hosts.
// Attempt timeouts are applied after admission by the transport.
func HTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (t *Transport) RoundTrip(request *http.Request) (result *http.Response, failure error) {
	defer func() {
		if failure != nil {
			record(request.Context(), Response{}, failure)
		}
	}()
	if len(request.URL.RawQuery) > t.maxQuery {
		return nil, application.ErrRequestTooLarge
	}
	q, err := parseQuery(request)
	if err != nil {
		return nil, err
	}
	market := ""
	if t.scope == Bybit {
		market = q.Get("category")
	}
	maximum, ok := t.pageLimits[market]
	if !ok {
		return nil, application.ErrUnsupportedOperation
	}
	cost, err := resolveCost(t.scope, request, maximum)
	if err != nil {
		return nil, err
	}
	operation, err := t.controller.operation(request.Context(), t.scope, cost.operation)
	if err != nil {
		return nil, err
	}
	var notBefore time.Time
	for attempt := 1; attempt <= t.settings.Retry.MaxAttempts; attempt++ {
		reservation, err := t.controller.acquire(request.Context(), t.scope, cost, operation, notBefore)
		if err != nil {
			return nil, err
		}
		if err := request.Context().Err(); err != nil {
			t.controller.rollback(t.scope, operation, reservation)
			return nil, err
		}
		started := reservation.at
		response, body, event, retryable := func() (*http.Response, []byte, Event, bool) {
			defer t.controller.release(t.scope, reservation)
			defer func() {
				if recovered := recover(); recovered != nil {
					if t.observe != nil {
						t.observe(Event{Scope: t.scope, Market: domain.Market(market), Operation: cost.operation,
							Path: request.URL.Path, StartedAt: started, Duration: max(0, t.clock.Now().Sub(started)), Error: application.ErrInternal})
					}
					panic(recovered)
				}
			}()
			return t.attempt(request, reservation, operation)
		}()
		if !reservation.dispatched {
			return nil, event.Error
		}
		if t.observe != nil {
			t.observe(event)
		}
		if event.Error == nil {
			response.Body = io.NopCloser(bytes.NewReader(body))
			response.ContentLength = int64(len(body))
			record(request.Context(), Response{Body: body, Header: response.Header.Clone(), Status: response.StatusCode, StartedAt: started, FetchedAt: started.Add(event.Duration)}, nil)
			return response, nil
		}
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		if !retryable || attempt == t.settings.Retry.MaxAttempts {
			return nil, event.Error
		}
		notBefore = t.clock.Now().Add(backoff(t.settings.Retry, attempt, t.jitter))
	}
	return nil, application.ErrUpstream
}

func (t *Transport) attempt(request *http.Request, reservation *entry, operation *operationState) (*http.Response, []byte, Event, bool) {
	started := reservation.at
	event := Event{Scope: t.scope, Operation: reservation.cost.operation, Path: request.URL.Path, StartedAt: started}
	if t.scope == Bybit {
		event.Market = domain.Market(request.URL.Query().Get("category"))
	}
	ctx, cancel := context.WithTimeout(request.Context(), t.settings.Timeout)
	defer cancel()

	attemptRequest := request.Clone(ctx)
	// A non-rewindable empty body prevents net/http from replaying a GET
	// inside RoundTrip. It sends no data; each retry must pass admission.
	// nil and http.NoBody would both allow implicit retries.
	attemptRequest.Body = io.NopCloser(bytes.NewReader(nil))
	attemptRequest.GetBody = nil
	if err := t.controller.dispatch(ctx, reservation); err != nil {
		t.controller.rollback(t.scope, operation, reservation)
		event.Error = err
		return nil, nil, event, false
	}
	observeAttempt(request.Context())
	response, err := t.base.RoundTrip(attemptRequest)

	var body []byte
	retryable := false
	if response != nil {
		event.Status = response.StatusCode
		event.Header = response.Header.Clone()
		t.responseHeaders(request, response, reservation)

		if response.Body != nil {
			reader, decodeErr := responseBody(response)
			if decodeErr != nil {
				_ = response.Body.Close()
				err = decodeErr
			} else {
				body, err = readBody(reader, t.settings.MaxResponseBytes, err)
			}
		} else if err == nil {
			err = application.ErrInvalidUpstreamData
		}
	}
	received := t.clock.Now()
	if err != nil {
		event.Error = fmt.Errorf("exchange transport: %w", errors.Join(application.ErrUpstream, err))
		retryable = temporary(err)
		if errors.Is(err, application.ErrInvalidUpstreamData) {
			event.Error = application.ErrInvalidUpstreamData
			if errors.Is(err, errResponseTooLarge) {
				event.FailureReason = "oversized_body"
			}
		}
	} else if response == nil {
		event.Error = application.ErrInvalidUpstreamData
	} else {
		event.Code, event.Error, retryable = t.classify(request, response, body, reservation)
	}
	// Headers must still extend cooldown when a body cannot be read or is too big.
	if response != nil && err != nil {
		t.rateSignal(request, response, body, event.Code)
	}
	if t.scope != Bybit && strings.HasSuffix(request.URL.Path, "/exchangeInfo") && event.Error != nil {
		if event.FailureReason == "" {
			event.FailureReason = "catalog_refresh_failed"
		}
		t.controller.catalogFailure(t.scope, started, event.FailureReason)
	}
	event.Duration = max(0, received.Sub(started))
	return response, body, event, retryable
}

func readBody(body io.ReadCloser, maximum int, transportError error) ([]byte, error) {
	defer func() { _ = body.Close() }()
	// The configured maximum fits int; avoid maximum+1 integer overflow.
	data, err := io.ReadAll(io.LimitReader(body, int64(maximum)))
	if err != nil {
		return nil, err
	}
	var extra [1]byte
	n, err := io.ReadFull(body, extra[:])
	if n > 0 {
		return nil, errResponseTooLarge
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return data, transportError
}

func temporary(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsTemporary {
		return true
	}
	var network net.Error
	return errors.As(err, &network) && network.Timeout()
}

func (c *Controller) rollback(scope Scope, operation *operationState, attempt *entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if attempt.dispatched {
		return
	}
	s := c.scopes[scope]
	for i, e := range s.history {
		if e == attempt {
			s.history = append(s.history[:i], s.history[i+1:]...)
			break
		}
	}
	if s.next.Equal(attempt.at.Add(s.spacing)) {
		s.next = time.Time{}
		if len(s.history) > 0 {
			s.next = s.history[len(s.history)-1].at.Add(s.spacing)
		}
	}
	attempt.inflight = false
	operation.attempts--
	c.active--
	c.lanes[laneIndex(scope, attempt.cost.operation)].active--
	c.notify()
}

func (c *Controller) dispatch(ctx context.Context, attempt *entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	attempt.dispatched = true
	return nil
}
