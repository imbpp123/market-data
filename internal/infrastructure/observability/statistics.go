package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/infrastructure/exchange/upstream"
)

// Sample contains only bounded operational labels, never a symbol or payload.
type Sample struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value"`
}

type Statistics struct {
	RPC         *RPC
	Admission   *upstream.Controller
	Instruments *Instruments
	Current     *Current
	Klines      *Klines
	Exchanges   *Exchanges
	Inventory   kline.Inventory
	Scopes      []application.Scope
}

func (s *Statistics) Snapshot(ctx context.Context) ([]Sample, error) {
	counts, err := s.Inventory.CandleCounts(ctx)
	if err != nil {
		return nil, err
	}
	result := s.RPC.Samples()
	add := func(name string, labels map[string]string, value float64) {
		result = append(result, Sample{Name: name, Labels: labels, Value: value})
	}
	publication := func(prefix string, labels map[string]string, total, failures uint64, at time.Time, size int) {
		add(prefix+"_refresh_total", labels, float64(total))
		add(prefix+"_refresh_errors_total", labels, float64(failures))
		timestamp := float64(0)
		if !at.IsZero() {
			timestamp = float64(at.UnixMilli()) / 1000
		}
		add("last_successful_"+prefix+"_fetch", labels, timestamp)
		add(prefix+"_snapshot_size", labels, float64(size))
	}
	instruments, current, klines := s.Instruments.Snapshot(), s.Current.Snapshot(), s.Klines.Snapshot()
	for _, scope := range s.Scopes {
		labels := scopeLabels(scope)
		i := instruments[scope]
		publication("instrument", labels, i.RefreshTotal, i.RefreshErrorsTotal, i.LastSuccessfulFetch, i.SnapshotSize)
		ticker := current[CurrentScope{Scope: scope}]
		publication("ticker", labels, ticker.RefreshTotal, ticker.RefreshErrorsTotal, ticker.LastSuccessfulFetch, ticker.SnapshotSize)
		stats := current[CurrentScope{Scope: scope, Window: 24 * time.Hour}]
		statsLabels := scopeLabels(scope)
		statsLabels["window"] = "24h"
		publication("market_stats", statsLabels, stats.RefreshTotal, stats.RefreshErrorsTotal, stats.LastSuccessfulFetch, stats.SnapshotSize)
		k := klines[scope]
		add("kline_cache_hits_total", labels, float64(k.CacheHits))
		add("kline_cache_misses_total", labels, float64(k.CacheMisses))
		add("kline_upstream_requests_total", labels, float64(k.Attempts))
		add("kline_candles_downloaded_total", labels, float64(k.Downloaded))
		add("singleflight_shared_total", labels, float64(k.SharedWaits))
		add("kline_count", labels, float64(counts[scope]))
		add("kline_fill_duration_seconds_count", labels, float64(k.Duration.Count))
		add("kline_fill_duration_seconds_sum", labels, k.Duration.Sum)
		add("kline_fill_duration_seconds_max", labels, k.Duration.Max)
	}
	for scope, stats := range s.Exchanges.Snapshot() {
		labels := scopeLabels(scope.Scope)
		labels["operation"] = scope.Operation
		add("exchange_requests_total", labels, float64(stats.Requests))
		add("exchange_errors_total", labels, float64(stats.Errors))
		add("exchange_oversized_bodies_total", labels, float64(stats.OversizedBodies))
		add("exchange_request_duration_seconds_count", labels, float64(stats.Duration.Count))
		add("exchange_request_duration_seconds_sum", labels, stats.Duration.Sum)
		add("exchange_request_duration_seconds_max", labels, stats.Duration.Max)
	}
	if s.Admission != nil {
		result = append(result, admissionSamples(s.Admission.Diagnostics())...)
	}
	slices.SortFunc(result, func(a, b Sample) int { return strings.Compare(a.Name+labelText(a.Labels), b.Name+labelText(b.Labels)) })
	return result, nil
}

func scopeLabels(scope application.Scope) map[string]string {
	return map[string]string{"exchange": string(scope.Exchange), "market": string(scope.Market)}
}

func labelText(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+strconv.Quote(labels[key]))
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func (s *Statistics) DebugHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("view") == "admission" {
			w.Header().Set("Content-Type", "application/json")
			if s.Admission == nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"statistics_unavailable","message":"Statistics unavailable"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(s.Admission.Diagnostics())
			return
		}
		snapshot, err := s.Snapshot(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"statistics_unavailable","message":"Statistics unavailable"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(snapshot)
	})
}

// PrometheusHandler uses the stable text 0.0.4 format. The fixed metric set does
// not need a second registry or a monitoring dependency for basic counters.
func (s *Statistics) PrometheusHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := s.Snapshot(r.Context())
		if err != nil {
			http.Error(w, "Statistics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		previous := ""
		for _, sample := range snapshot {
			if sample.Name != previous {
				kind := "gauge"
				if strings.HasSuffix(sample.Name, "_total") || strings.HasSuffix(sample.Name, "_count") && sample.Name != "kline_count" || strings.HasSuffix(sample.Name, "_sum") {
					kind = "counter"
				}
				_, _ = fmt.Fprintf(w, "# TYPE %s %s\n", sample.Name, kind)
				previous = sample.Name
			}
			_, _ = fmt.Fprintf(w, "%s%s %s\n", sample.Name, labelText(sample.Labels), strconv.FormatFloat(sample.Value, 'g', -1, 64))
		}
	})
}
