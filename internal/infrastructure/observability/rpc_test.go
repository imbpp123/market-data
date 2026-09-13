package observability

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	grpctransport "market-data/internal/transport/grpc"
)

func TestRPCObservationsTrackLifetimeAndBoundLabels(t *testing.T) {
	metrics := NewRPC()
	finish := metrics.Start(t.Context(), "secret symbol")
	assert.Contains(t, metrics.Samples(), Sample{Name: "rpc_active", Labels: map[string]string{"method": "unknown"}, Value: 1})
	finish(grpctransport.Event{Method: "secret symbol", Code: codes.Code(9999), Reason: "secret payload", Duration: 2 * time.Second, RequestBytes: 10, ResponseBytes: 20})
	samples := metrics.Samples()
	labels := map[string]string{"method": "unknown", "status": "Unknown", "reason": "unknown"}
	assert.Contains(t, samples, Sample{Name: "rpc_active", Labels: map[string]string{"method": "unknown"}, Value: 0})
	assert.Contains(t, samples, Sample{Name: "rpc_total", Labels: labels, Value: 1})
	assert.Contains(t, samples, Sample{Name: "rpc_request_bytes_total", Labels: labels, Value: 10})
	assert.Contains(t, samples, Sample{Name: "rpc_response_bytes_total", Labels: labels, Value: 20})
	assert.Contains(t, samples, Sample{Name: "rpc_duration_seconds_sum", Labels: labels, Value: 2})
	samples[0].Labels["method"] = "mutation"
	assert.NotContains(t, metrics.Samples(), samples[0])
}
