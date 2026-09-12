package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"market-data/internal/application"
)

// Response retains the exact bounded body before SDK decoding, including numbers.
// No SDK objects are part of this adapter boundary.
type Response struct {
	Body      json.RawMessage
	Header    http.Header
	Status    int
	StartedAt time.Time
	FetchedAt time.Time
}

type captureKey struct{}
type capture struct {
	mu       sync.Mutex
	response Response
	err      error
	received bool
}

// Execute isolates metadata for one SDK invocation. Some SDKs turn transport
// errors into plain strings; the captured error preserves application identity.
func Execute(ctx context.Context, call func(context.Context) error) (Response, error) {
	result := &capture{}
	err := call(context.WithValue(ctx, captureKey{}, result))
	result.mu.Lock()
	defer result.mu.Unlock()
	if result.err != nil {
		return Response{}, result.err
	}
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, application.ErrInvalidUpstreamData
	}
	if !result.received {
		return Response{}, application.ErrInvalidUpstreamData
	}
	return result.response, nil
}

func record(ctx context.Context, response Response, err error) {
	result, _ := ctx.Value(captureKey{}).(*capture)
	if result == nil {
		return
	}
	result.mu.Lock()
	defer result.mu.Unlock()
	result.response = response
	result.err = err
	result.received = true
}
