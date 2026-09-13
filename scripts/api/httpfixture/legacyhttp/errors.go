package httptransport

import (
	"context"
	"errors"
	"net/http"

	"market-data/internal/application"
)

// The response observer receives the public code and original cause separately.
// This keeps cancellation identity without exposing errors in the response body.
type errorObserver interface {
	ObserveHTTPError(code string, cause error)
}

func writeApplicationError(w http.ResponseWriter, err error) {
	failure := application.ErrInternal
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		failure = application.ErrRequestTimeout
	} else {
		var known *application.Error
		if errors.As(err, &known) {
			failure = known
		}
	}

	status := http.StatusInternalServerError
	switch failure {
	case application.ErrInvalidParameter, application.ErrInvalidFilter, application.ErrInvalidStatus,
		application.ErrUnsupportedWindow, application.ErrInvalidInterval, application.ErrInvalidRange,
		application.ErrRequestTooLarge, application.ErrRangeOutOfRetention:
		status = http.StatusBadRequest
	case application.ErrSymbolNotFound:
		status = http.StatusNotFound
	case application.ErrDataNotReady, application.ErrServiceOverloaded, application.ErrUpstreamUnavailable:
		status = http.StatusServiceUnavailable
	case application.ErrRequestTimeout:
		status = http.StatusGatewayTimeout
	case application.ErrUpstream, application.ErrInvalidUpstreamData, application.ErrUpstreamAttemptLimit, application.ErrIncompleteData:
		status = http.StatusBadGateway
	default:
		failure = application.ErrInternal
	}

	writeErrorWithCause(w, status, failure.Code(), failure.Error(), err)
}
