package httptransport

// The observer receives operational errors without exposing internal causes.
type errorObserver interface {
	ObserveHTTPError(code string, cause error)
}
