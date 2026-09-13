package upstream

import (
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func diagnosticWindow(t *testing.T, c *Controller, name string) WindowDiagnostic {
	t.Helper()
	for _, s := range c.Diagnostics().Scopes {
		if s.Scope != BinanceSpot {
			continue
		}
		for _, w := range s.Windows {
			if w.Name == name {
				return w
			}
		}
	}
	t.Fatalf("missing diagnostic window %s", name)
	return WindowDiagnostic{}
}

func TestDiagnosticsShowInputsUsageAndTimeRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg, err := config.Load(strings.NewReader(`upstream: {binance: {stop_threshold_percent: 85}, limits: {binance_spot: {windows: {request_weight_1m: {limit: 5000}}}}}`), nil)
		require.NoError(t, err)
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			return response(200, http.Header{"X-Mbx-Used-Weight-1m": {"4251"}}, string(catalogWeight(10000))), nil
		}}
		c, transport := setup(t, cfg, BinanceSpot, base)
		started := time.Now()
		bootstrap := diagnosticWindow(t, c, "request_weight_1m")
		assert.Equal(t, "bootstrap", bootstrap.Source)
		assert.True(t, bootstrap.IncompleteHistory)
		assert.Zero(t, bootstrap.Accounted)
		_, err = send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")
		require.NoError(t, err)
		c.Cooldown(BinanceSpot, started.Add(2*time.Minute))

		d := diagnosticWindow(t, c, "request_weight_1m")

		assert.Equal(t, "exchange_info", d.Source)
		assert.Equal(t, 10000, d.ExchangeLimit)
		assert.Equal(t, 5000, d.UserCap)
		assert.Equal(t, 85, d.ThresholdPercent)
		assert.Equal(t, 4250, d.StopLine)
		assert.Equal(t, 4251, d.Accounted)
		assert.Equal(t, 4251, d.Observed)
		assert.Equal(t, 20, d.Local)
		assert.Zero(t, d.Reserved)
		assert.Zero(t, d.Remaining)
		assert.True(t, d.Uncertain)
		assert.Equal(t, started, d.UpdatedAt)
		assert.Equal(t, started, d.HistorySince)
		assert.ElementsMatch(t, []string{"common_threshold", "exchange_cooldown"}, d.Operations[Klines].Reasons)
		assert.Equal(t, started.Add(2*time.Minute), d.Operations[Klines].NextEligible)
		before := c.Diagnostics()
		before.Scopes[0].Windows[0].Operations[0].Reasons = append(before.Scopes[0].Windows[0].Operations[0].Reasons, "mutated")
		assert.NotEqual(t, before, c.Diagnostics())
		assert.Len(t, base.sent(), 1)

		time.Sleep(time.Minute)
		d = diagnosticWindow(t, c, "request_weight_1m")
		assert.Zero(t, d.Accounted)
		assert.Equal(t, float64(60), d.AgeSeconds)
		assert.False(t, d.IncompleteHistory)
		assert.Equal(t, []string{"exchange_cooldown"}, d.Operations[Klines].Reasons)
		time.Sleep(time.Minute)
		d = diagnosticWindow(t, c, "request_weight_1m")
		assert.Empty(t, d.Operations[Klines].Reasons)
		assert.Equal(t, time.Now(), d.Operations[Klines].NextEligible)
		assert.Len(t, base.sent(), 1, "reading diagnostics must not send probes")
	})
}

func TestDiagnosticsMatchInFlightCrossingAndQuietRejection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reply := make(chan struct{})
		base := &recorder{handle: func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/api/v3/exchangeInfo" {
				return response(200, http.Header{"X-Mbx-Used-Weight-1m": {"5400"}}, string(catalogWeight(6000))), nil
			}
			<-reply
			return response(200, nil, `[]`), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		var events []DiagnosticEvent
		c.SetDiagnosticObserver(func(e DiagnosticEvent) { events = append(events, e) })
		_, err := send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")
		require.NoError(t, err)
		done := make(chan error, 1)
		ctx := begin(t, c, BinanceSpot, Klines)
		go func() {
			_, err := send(ctx, transport, "/api/v3/klines?limit=1")
			done <- err
		}()
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()

		d := diagnosticWindow(t, c, "request_weight_1m")

		assert.Equal(t, 5402, d.Accounted)
		assert.Equal(t, 2, d.Reserved)
		assert.Equal(t, 22, d.Local)
		assert.Equal(t, 1618, d.Operations[Klines].Remaining)
		assert.Contains(t, d.Operations[Klines].Reasons, "common_threshold")
		for range 5 {
			_, err := send(begin(t, c, BinanceSpot, Klines), transport, "/api/v3/klines?limit=1")
			require.ErrorIs(t, err, application.ErrServiceOverloaded)
		}
		require.Len(t, events, 2, "catalog transition and one budget transition")
		require.Len(t, events[0].Limits, 1)
		assert.Equal(t, "exchange_info", events[0].Limits[0].Source)
		assert.Equal(t, 6000, events[0].Limits[0].ExchangeLimit)
		assert.Equal(t, 5400, events[0].Limits[0].StopLine)
		assert.Equal(t, 90, events[0].ThresholdPercent)
		assert.Equal(t, "common_threshold", events[1].Reason)
		assert.Equal(t, 2, events[1].RequestWeight)
		assert.Len(t, base.sent(), 2)
		assert.Equal(t, d, diagnosticWindow(t, c, "request_weight_1m"))
		close(reply)
		require.NoError(t, <-done)
		assert.Zero(t, diagnosticWindow(t, c, "request_weight_1m").Reserved)
		time.Sleep(time.Minute)
		_, err = send(begin(t, c, BinanceSpot, Klines), transport, "/api/v3/klines?limit=1")
		require.NoError(t, err)
		require.Len(t, events, 3)
		assert.Equal(t, "request_admitted", events[2].Reason)
		assert.Equal(t, 2, events[2].RequestWeight)
	})
}

func TestDiagnosticsSeparateShareAndImpossibleCost(t *testing.T) {
	cases := []struct {
		name   string
		limit  int
		spent  int
		reason string
	}{
		{name: "strict share", limit: 6000, spent: 251, reason: "operation_share"},
		{name: "valid reduction", limit: 10, reason: "request_cost_exceeds_allowance"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(config.Defaults(), SystemClock{})
			require.NoError(t, err)
			now := time.Now()
			require.NoError(t, c.updateCatalog(BinanceSpot, now, catalogWeight(tt.limit)))
			c.scopes[BinanceSpot].history = []*entry{{at: now, cost: cost{operation: Instruments, weight: tt.spent}}}

			d := diagnosticWindow(t, c, "request_weight_1m").Operations[Instruments]

			assert.Equal(t, []string{tt.reason}, d.Reasons)
			assert.Equal(t, 20, d.RequestCost)
			if tt.spent > 0 {
				assert.Equal(t, now.Add(time.Minute), d.NextEligible)
			} else {
				assert.True(t, d.NextEligible.IsZero())
			}
		})
	}
}

func TestCatalogFailureRecoveryKeepsUsageAndClearsStaleError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bodies := []string{`{"rateLimits":[`, string(catalogWeight(10000)), string(catalogWeight(10000))}
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			body := bodies[0]
			bodies = bodies[1:]
			return response(200, nil, body), nil
		}}
		c, transport := setup(t, config.Defaults(), BinanceSpot, base)
		var events []DiagnosticEvent
		c.SetDiagnosticObserver(func(e DiagnosticEvent) { events = append(events, e) })
		started := time.Now()
		_, err := send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")
		require.ErrorIs(t, err, application.ErrUpstreamUnavailable)
		assert.Equal(t, "catalog_refresh_failed", c.Diagnostics().Scopes[0].CatalogError)
		assert.Equal(t, started, c.Diagnostics().Scopes[0].CatalogErrorAt)
		_, err = send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")
		require.NoError(t, err)
		assert.Empty(t, c.Diagnostics().Scopes[0].CatalogError)
		assert.True(t, c.Diagnostics().Scopes[0].CatalogErrorAt.IsZero())
		assert.Equal(t, 40, diagnosticWindow(t, c, "request_weight_1m").Local)
		// A late failed request cannot undo this accepted recovery.
		c.catalogFailure(BinanceSpot, started, "oversized_body")
		assert.Empty(t, c.Diagnostics().Scopes[0].CatalogError)
		_, err = send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")
		require.NoError(t, err)
		assert.Len(t, events, 2, "unchanged valid catalogs do not repeat state logs")
		assert.Equal(t, 60, diagnosticWindow(t, c, "request_weight_1m").Local)
	})
}

func TestOversizedCatalogRetainsDistinctReasonAndCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Defaults()
		cfg.HTTPClient.MaxResponseBytes = 64
		base := &recorder{handle: func(*http.Request) (*http.Response, error) {
			return response(418, http.Header{"Retry-After": {"120"}}, strings.Repeat(" ", 65)), nil
		}}
		c, transport := setup(t, cfg, BinanceSpot, base)
		var events []Event
		transport.observe = func(e Event) { events = append(events, e) }
		started := time.Now()

		_, err := send(begin(t, c, BinanceSpot, Instruments), transport, "/api/v3/exchangeInfo")

		require.ErrorIs(t, err, application.ErrInvalidUpstreamData)
		require.Len(t, events, 1)
		assert.Equal(t, "oversized_body", events[0].FailureReason)
		d := c.Diagnostics().Scopes[0]
		assert.Equal(t, "oversized_body", d.CatalogError)
		assert.Equal(t, started, d.CatalogErrorAt)
		assert.Equal(t, started.Add(2*time.Minute), d.CooldownUntil)
		assert.Equal(t, 20, diagnosticWindow(t, c, "request_weight_1m").Local)
	})
}

func TestLateValidCatalogDoesNotClearNewerRefreshFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := New(config.Defaults(), SystemClock{})
		require.NoError(t, err)
		first := time.Now()
		time.Sleep(time.Second)
		failed := time.Now()
		c.catalogFailure(BinanceSpot, failed, "catalog_refresh_failed")

		require.NoError(t, c.updateCatalog(BinanceSpot, first, catalogWeight(10000)))

		d := c.Diagnostics().Scopes[0]
		assert.Equal(t, "catalog_refresh_failed", d.CatalogError)
		assert.Equal(t, failed, d.CatalogErrorAt)
		assert.Equal(t, 10000, diagnosticWindow(t, c, "request_weight_1m").ExchangeLimit)
		time.Sleep(time.Second)
		var events []DiagnosticEvent
		c.SetDiagnosticObserver(func(e DiagnosticEvent) { events = append(events, e) })
		require.NoError(t, c.updateCatalog(BinanceSpot, time.Now(), catalogWeight(10000)))
		assert.Empty(t, c.Diagnostics().Scopes[0].CatalogError)
		require.Len(t, events, 1)
		assert.Equal(t, "catalog_recovered", events[0].Reason)
	})
}
