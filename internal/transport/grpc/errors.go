// Package grpctransport exposes the generated market data API.
package grpctransport

import (
	"context"
	"errors"

	"market-data/internal/application"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errResponseTooLarge = errors.New("response is too large")

func mapError(err error) error {
	if err == nil {
		return nil
	}
	code, reason, message := codes.Internal, "internal_error", "Internal error"
	switch {
	case errors.Is(err, context.Canceled):
		code, reason, message = codes.Canceled, "request_canceled", "Request canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, application.ErrRequestTimeout):
		code, reason, message = codes.DeadlineExceeded, "request_timeout", "Request timed out"
	case errors.Is(err, errResponseTooLarge):
		code, reason, message = codes.ResourceExhausted, "response_too_large", "Response is too large"
	default:
		mappings := []struct {
			code   codes.Code
			errors []*application.Error
		}{
			{codes.InvalidArgument, []*application.Error{application.ErrInvalidParameter, application.ErrInvalidFilter, application.ErrInvalidStatus, application.ErrUnsupportedWindow, application.ErrInvalidInterval, application.ErrInvalidRange, application.ErrRequestTooLarge, application.ErrRangeOutOfRetention}},
			{codes.NotFound, []*application.Error{application.ErrSymbolNotFound}},
			{codes.Unavailable, []*application.Error{application.ErrDataNotReady, application.ErrUpstreamUnavailable, application.ErrUpstream}},
			{codes.ResourceExhausted, []*application.Error{application.ErrServiceOverloaded, application.ErrUpstreamAttemptLimit}},
			{codes.FailedPrecondition, []*application.Error{application.ErrIncompleteData}},
			{codes.DataLoss, []*application.Error{application.ErrInvalidUpstreamData}},
			{codes.Unimplemented, []*application.Error{application.ErrUnsupportedOperation}},
		}
		for _, mapping := range mappings {
			for _, known := range mapping.errors {
				if errors.Is(err, known) {
					code, reason, message = mapping.code, known.Code(), known.Error()
				}
			}
		}
	}
	result, detailErr := status.New(code, message).WithDetails(&pb.ErrorDetail{Reason: reason})
	if detailErr != nil {
		return status.Error(codes.Internal, "Internal error")
	}
	return result.Err()
}
