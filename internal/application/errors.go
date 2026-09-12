package application

// Error is a stable application failure. Wrap these values with %w for internal
// context; transport must expose the stable message, not the wrapped details.
type Error struct {
	code    string
	message string
}

func (e *Error) Error() string { return e.message }
func (e *Error) Code() string  { return e.code }

var (
	ErrInvalidParameter     = &Error{"invalid_parameter", "Invalid parameter"}
	ErrInvalidFilter        = &Error{"invalid_filter", "Invalid filter"}
	ErrInvalidStatus        = &Error{"invalid_status", "Invalid instrument status"}
	ErrUnsupportedWindow    = &Error{"unsupported_window", "Unsupported statistics window"}
	ErrInvalidInterval      = &Error{"invalid_interval", "Invalid or unsupported interval"}
	ErrInvalidRange         = &Error{"invalid_range", "Invalid time range"}
	ErrRequestTooLarge      = &Error{"request_too_large", "Request is too large"}
	ErrRangeOutOfRetention  = &Error{"range_out_of_retention", "Range is outside retained history"}
	ErrSymbolNotFound       = &Error{"symbol_not_found", "Symbol not found"}
	ErrDataNotReady         = &Error{"data_not_ready", "Data is not ready"}
	ErrServiceOverloaded    = &Error{"service_overloaded", "Service is overloaded"}
	ErrUpstreamUnavailable  = &Error{"upstream_unavailable", "Upstream is unavailable"}
	ErrRequestTimeout       = &Error{"request_timeout", "Request timed out"}
	ErrUpstream             = &Error{"upstream_error", "Upstream request failed"}
	ErrInvalidUpstreamData  = &Error{"invalid_upstream_data", "Invalid upstream data"}
	ErrUpstreamAttemptLimit = &Error{"upstream_attempt_limit", "Upstream attempt limit reached"}
	ErrIncompleteData       = &Error{"incomplete_data", "Requested data is incomplete"}
	ErrInternal             = &Error{"internal_error", "Internal error"}
	ErrUnsupportedOperation = &Error{"unsupported_operation", "Unsupported operation"}
)
