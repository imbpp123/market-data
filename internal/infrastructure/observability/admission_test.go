package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"market-data/internal/config"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmissionMetricsBoundDiscoveredWindowsWithoutDuplicateSeries(t *testing.T) {
	at := time.Unix(1000, 0)
	d := upstream.AdmissionSnapshot{At: at, Scopes: []upstream.ScopeDiagnostic{{Scope: upstream.BinanceSpot,
		CatalogError: "oversized_body", CatalogErrorAt: at, Windows: []upstream.WindowDiagnostic{
			{Name: "request_weight_1m", Unit: "weight", Source: "exchange_info", ExchangeLimit: 10000, StopLine: 9000,
				Observed: 9001, Local: 2, Accounted: 9001, Reserved: 2, Uncertain: true,
				Operations: []upstream.OperationDiagnostic{{Operation: "klines", RequestCost: 2, Share: 2700, Local: 2, Remaining: 2698,
					Reasons: []string{"common_threshold"}, NextEligible: at.Add(time.Minute)}}},
			{Name: "weight_7200000000000", Unit: "weight", Operations: []upstream.OperationDiagnostic{{Operation: "klines", Reasons: []string{"common_threshold"}, NextEligible: at.Add(2 * time.Hour)}}},
			{Name: "requests_3600000000000", Unit: "requests", Operations: []upstream.OperationDiagnostic{{Operation: "klines", Reasons: []string{"operation_share"}}}},
			{Name: "weight_10800000000000", Unit: "weight"},
		}}}}

	samples := admissionSamples(d)

	seen := map[string]bool{}
	for _, s := range samples {
		key := s.Name + labelText(s.Labels)
		assert.False(t, seen[key], "duplicate metric %s", key)
		seen[key] = true
		for key, value := range s.Labels {
			switch key {
			case "scope":
				assert.Equal(t, "binance_spot", value)
			case "window":
				assert.Equal(t, "request_weight_1m", value)
			case "unit":
				assert.Contains(t, []string{"weight", "requests"}, value)
			case "operation":
				assert.Contains(t, []string{"tickers", "klines", "instruments", "market_stats"}, value)
			case "reason":
				assert.Contains(t, []string{"catalog_refresh_failed", "oversized_body", "common_threshold", "operation_share", "exchange_cooldown", "request_cost_exceeds_allowance"}, value)
			case "source":
				assert.Contains(t, []string{"bootstrap", "exchange_info"}, value)
			default:
				t.Errorf("unexpected label %s", key)
			}
		}
	}
	assert.Contains(t, samples, Sample{Name: "admission_discovered_windows", Labels: map[string]string{"scope": "binance_spot", "unit": "weight"}, Value: 2})
	assert.Contains(t, samples, Sample{Name: "admission_blocking_windows", Labels: map[string]string{"scope": "binance_spot", "operation": "klines", "reason": "common_threshold"}, Value: 2})
	assert.Contains(t, samples, Sample{Name: "admission_next_eligible_timestamp_seconds", Labels: map[string]string{"scope": "binance_spot", "operation": "klines"}, Value: 0})
	assert.Contains(t, samples, Sample{Name: "admission_catalog_error", Labels: map[string]string{"scope": "binance_spot", "reason": "oversized_body"}, Value: 1})
}

func TestAdmissionDebugAndMetricsUseSameControllerState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stats := testStatistics()
		controller, err := upstream.New(config.Defaults(), upstream.SystemClock{})
		require.NoError(t, err)
		stats.Admission = controller
		controller.Cooldown(upstream.BinanceSpot, time.Now().Add(time.Minute))
		response := httptest.NewRecorder()

		stats.DebugHandler().ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/debug/stats?view=admission", nil))

		require.Equal(t, http.StatusOK, response.Code)
		var snapshot upstream.AdmissionSnapshot
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &snapshot))
		expected, err := json.Marshal(controller.Diagnostics())
		require.NoError(t, err)
		assert.JSONEq(t, string(expected), response.Body.String())
		metrics, err := stats.Snapshot(t.Context())
		require.NoError(t, err)
		for _, sample := range admissionSamples(snapshot) {
			assert.Contains(t, metrics, sample)
		}
		assert.Empty(t, stats.Exchanges.Snapshot(), "diagnostic reads are not HTTP attempts")
	})
}

func TestOversizedAttemptHasOneExchangeErrorAndSeparateCounter(t *testing.T) {
	stats := NewExchanges()
	stats.Observe(upstream.Event{Scope: upstream.BinanceSpot, Operation: upstream.Instruments, Error: assert.AnError, FailureReason: "oversized_body"})

	for _, s := range stats.Snapshot() {
		assert.Equal(t, uint64(1), s.Requests)
		assert.Equal(t, uint64(1), s.Errors)
		assert.Equal(t, uint64(1), s.OversizedBodies)
	}
}
