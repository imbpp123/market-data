package httptransport

import (
	"encoding/json"
	"net/http"
)

// NewHandler exposes local health routes.
func NewHandler(ready func() bool, maxQueryBytes int) http.Handler {
	return NewAPIHandler(ready, maxQueryBytes, nil)
}

func NewAPIHandler(ready func() bool, maxQueryBytes int, routes map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		route := routes[r.URL.Path]
		if r.URL.Path != "/health" && r.URL.Path != "/ready" && route == nil {
			writeError(w, http.StatusNotFound, "not_found", "Route not found")

			return
		}

		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")

			return
		}

		if len(r.URL.RawQuery) > maxQueryBytes {
			writeError(w, http.StatusRequestURITooLong, "request_too_large", "Query is too large")

			return
		}

		if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "GET body is not allowed")

			return
		}

		if route != nil {
			route.ServeHTTP(w, r)
			return
		}

		if r.URL.Path == "/ready" && !ready() {
			writeError(w, http.StatusServiceUnavailable, "data_not_ready", "Local initialization is not complete")

			return
		}

		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorWithCause(w, status, code, message, nil)
}

func writeErrorWithCause(w http.ResponseWriter, status int, code, message string, cause error) {
	if observer, ok := w.(errorObserver); ok {
		observer.ObserveHTTPError(code, cause)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
