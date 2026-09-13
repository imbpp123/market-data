package grpctransport

import (
	"context"
	"errors"
	"fmt"
	"testing"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"market-data/internal/application"
)

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		code   codes.Code
		reason string
	}{
		{application.ErrInvalidParameter, codes.InvalidArgument, "invalid_parameter"}, {application.ErrInvalidFilter, codes.InvalidArgument, "invalid_filter"}, {application.ErrInvalidStatus, codes.InvalidArgument, "invalid_status"}, {application.ErrUnsupportedWindow, codes.InvalidArgument, "unsupported_window"}, {application.ErrInvalidInterval, codes.InvalidArgument, "invalid_interval"}, {application.ErrInvalidRange, codes.InvalidArgument, "invalid_range"}, {application.ErrRequestTooLarge, codes.InvalidArgument, "request_too_large"}, {application.ErrRangeOutOfRetention, codes.InvalidArgument, "range_out_of_retention"},
		{application.ErrSymbolNotFound, codes.NotFound, "symbol_not_found"}, {application.ErrDataNotReady, codes.Unavailable, "data_not_ready"}, {application.ErrUpstreamUnavailable, codes.Unavailable, "upstream_unavailable"}, {application.ErrUpstream, codes.Unavailable, "upstream_error"}, {application.ErrServiceOverloaded, codes.ResourceExhausted, "service_overloaded"}, {application.ErrUpstreamAttemptLimit, codes.ResourceExhausted, "upstream_attempt_limit"}, {errResponseTooLarge, codes.ResourceExhausted, "response_too_large"}, {application.ErrIncompleteData, codes.FailedPrecondition, "incomplete_data"}, {application.ErrInvalidUpstreamData, codes.DataLoss, "invalid_upstream_data"}, {application.ErrRequestTimeout, codes.DeadlineExceeded, "request_timeout"}, {context.DeadlineExceeded, codes.DeadlineExceeded, "request_timeout"}, {context.Canceled, codes.Canceled, "request_canceled"}, {application.ErrUnsupportedOperation, codes.Unimplemented, "unsupported_operation"}, {application.ErrInternal, codes.Internal, "internal_error"}, {errors.New("secret upstream body"), codes.Internal, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.reason+"/"+tc.err.Error(), func(t *testing.T) {
			result := status.Convert(mapError(fmt.Errorf("secret context: %w", tc.err)))
			assert.Equal(t, tc.code, result.Code())
			assert.NotContains(t, result.Message(), "secret")
			require.Len(t, result.Details(), 1)
			detail, ok := result.Details()[0].(*pb.ErrorDetail)
			require.True(t, ok)
			assert.Equal(t, tc.reason, detail.Reason)
		})
	}
}
