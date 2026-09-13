package observability

import (
	"context"
	"sync"

	"google.golang.org/grpc/codes"
	grpctransport "market-data/internal/transport/grpc"
)

type rpcKey struct{ method, code, reason string }

type rpcTotals struct {
	count, requestBytes, responseBytes uint64
	duration                           DurationStats
}

type RPC struct {
	mu     sync.Mutex
	active map[string]int
	totals map[rpcKey]rpcTotals
}

func NewRPC() *RPC { return &RPC{active: make(map[string]int), totals: make(map[rpcKey]rpcTotals)} }

func (s *RPC) Start(_ context.Context, method string) func(grpctransport.Event) {
	switch method {
	case "ListInstruments", "ListTickers", "ListMarketStats", "GetKlines":
	default:
		method = "unknown"
	}
	s.mu.Lock()
	s.active[method]++
	s.mu.Unlock()
	return func(event grpctransport.Event) {
		reason := event.Reason
		switch reason {
		case "", "invalid_parameter", "invalid_filter", "invalid_status", "unsupported_window", "invalid_interval", "invalid_range", "request_too_large", "range_out_of_retention", "symbol_not_found", "data_not_ready", "upstream_unavailable", "upstream_error", "service_overloaded", "upstream_attempt_limit", "response_too_large", "incomplete_data", "invalid_upstream_data", "request_timeout", "request_canceled", "unsupported_operation", "internal_error", "transport_error":
		default:
			reason = "unknown"
		}
		code := event.Code
		if code > codes.Unauthenticated {
			code = codes.Unknown
		}
		key := rpcKey{method, code.String(), reason}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.active[method]--
		totals := s.totals[key]
		totals.count++
		totals.requestBytes += uint64(max(0, event.RequestBytes))
		totals.responseBytes += uint64(max(0, event.ResponseBytes))
		totals.duration.add(event.Duration)
		s.totals[key] = totals
	}
}

func (s *RPC) Samples() []Sample {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []Sample
	for method, active := range s.active {
		result = append(result, Sample{Name: "rpc_active", Labels: map[string]string{"method": method}, Value: float64(active)})
	}
	for key, totals := range s.totals {
		labels := map[string]string{"method": key.method, "status": key.code, "reason": key.reason}
		result = append(result, Sample{Name: "rpc_total", Labels: labels, Value: float64(totals.count)}, Sample{Name: "rpc_request_bytes_total", Labels: labels, Value: float64(totals.requestBytes)}, Sample{Name: "rpc_response_bytes_total", Labels: labels, Value: float64(totals.responseBytes)}, Sample{Name: "rpc_duration_seconds_count", Labels: labels, Value: float64(totals.duration.Count)}, Sample{Name: "rpc_duration_seconds_sum", Labels: labels, Value: totals.duration.Sum}, Sample{Name: "rpc_duration_seconds_max", Labels: labels, Value: totals.duration.Max})
	}
	return result
}
