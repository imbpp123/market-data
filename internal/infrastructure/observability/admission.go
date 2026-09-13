package observability

import (
	"slices"
	"time"

	"market-data/internal/infrastructure/exchange/upstream"
)

// Only reviewed window names become metric labels. Discovered windows are
// counted by unit; their separate limits and usage remain in the JSON view.
func admissionSamples(snapshot upstream.AdmissionSnapshot) []Sample {
	var result []Sample
	add := func(name string, labels map[string]string, value float64) {
		result = append(result, Sample{Name: "admission_" + name, Labels: labels, Value: value})
	}
	for _, scope := range snapshot.Scopes {
		labels := map[string]string{"scope": string(scope.Scope)}
		add("catalog_updated_timestamp_seconds", labels, timestamp(scope.CatalogUpdatedAt))
		add("catalog_error_timestamp_seconds", labels, timestamp(scope.CatalogErrorAt))
		add("cooldown_until_timestamp_seconds", labels, timestamp(scope.CooldownUntil))
		for _, reason := range []string{"catalog_refresh_failed", "oversized_body"} {
			add("catalog_error", map[string]string{"scope": string(scope.Scope), "reason": reason}, flag(scope.CatalogError == reason))
		}
		for _, unit := range []string{"weight", "requests"} {
			discovered := 0
			for _, w := range scope.Windows {
				if w.Unit == unit && !metricWindow(w.Name) {
					discovered++
				}
			}
			add("discovered_windows", map[string]string{"scope": string(scope.Scope), "unit": unit}, float64(discovered))
		}
		for _, w := range scope.Windows {
			if !metricWindow(w.Name) {
				continue
			}
			labels := map[string]string{"scope": string(scope.Scope), "window": w.Name}
			for name, value := range map[string]float64{
				"window_seconds": w.WindowSeconds, "limit_age_seconds": w.AgeSeconds,
				"limit_updated_timestamp_seconds": timestamp(w.UpdatedAt), "exchange_limit": float64(w.ExchangeLimit),
				"user_cap": float64(w.UserCap), "threshold_percent": float64(w.ThresholdPercent), "stop_line": float64(w.StopLine),
				"observed_usage": float64(w.Observed), "local_usage": float64(w.Local), "accounted_usage": float64(w.Accounted),
				"reserved_cost": float64(w.Reserved), "remaining_allowance": float64(w.Remaining),
				"uncertain": flag(w.Uncertain), "incomplete_history": flag(w.IncompleteHistory),
				"history_since_timestamp_seconds": timestamp(w.HistorySince),
			} {
				add(name, labels, value)
			}
			for _, source := range []string{"bootstrap", "exchange_info"} {
				add("limit_source", map[string]string{"scope": string(scope.Scope), "window": w.Name, "source": source}, flag(w.Source == source))
			}
			for _, op := range w.Operations {
				labels := map[string]string{"scope": string(scope.Scope), "window": w.Name, "operation": op.Operation}
				add("operation_request_cost", labels, float64(op.RequestCost))
				add("operation_share", labels, float64(op.Share))
				add("operation_local_usage", labels, float64(op.Local))
				add("operation_remaining_allowance", labels, float64(op.Remaining))
			}
		}
		for _, operation := range []string{"tickers", "klines", "instruments", "market_stats"} {
			next := snapshot.At
			unknown := false
			counts := map[string]int{"common_threshold": 0, "operation_share": 0, "request_cost_exceeds_allowance": 0, "exchange_cooldown": 0}
			for _, w := range scope.Windows {
				for _, op := range w.Operations {
					if op.Operation != operation {
						continue
					}
					unknown = unknown || op.NextEligible.IsZero()
					if op.NextEligible.After(next) {
						next = op.NextEligible
					}
					for reason := range counts {
						if slices.Contains(op.Reasons, reason) {
							counts[reason]++
						}
					}
				}
			}
			if unknown {
				next = time.Time{}
			}
			add("next_eligible_timestamp_seconds", map[string]string{"scope": string(scope.Scope), "operation": operation}, timestamp(next))
			for reason, count := range counts {
				add("blocking_windows", map[string]string{"scope": string(scope.Scope), "operation": operation, "reason": reason}, float64(count))
			}
		}
	}
	return result
}

func metricWindow(name string) bool {
	return name == "request_weight_1m" || name == "raw_requests_5m" || name == "funding_requests_5m"
}

func flag(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func timestamp(at time.Time) float64 {
	if at.IsZero() {
		return 0
	}
	return float64(at.UnixMilli()) / 1000
}
