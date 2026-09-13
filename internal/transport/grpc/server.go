package grpctransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"market-data/internal/application"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Settings struct {
	SnapshotTimeout   time.Duration
	KlineTimeout      time.Duration
	WriteGrace        time.Duration
	MaxSnapshots      int
	MaxKlines         int
	MaxRequestBytes   int
	MaxResponseBytes  int
	MaxHeaderBytes    int
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
}

type Event struct {
	Method           string
	Code             codes.Code
	Reason           string
	Duration         time.Duration
	RequestBytes     int
	ResponseBytes    int
	Error            error
	Panic            bool
	TransportFailure bool
}

// Observer starts at admission and ends after processing and stream completion.
// It must be safe for concurrent calls and must not block or panic.
type Observer func(context.Context, string) func(Event)

type Server struct {
	admission sync.Mutex
	http      *http.Server
	grpc      *grpc.Server
	settings  Settings
	snapshots chan struct{}
	klines    chan struct{}
	observer  Observer
	stopped   atomic.Bool
	owned     sync.WaitGroup
}

type callKey struct{}

type call struct {
	mu             sync.Mutex
	event          Event
	deadline       time.Time
	reject         error
	closed         bool
	expectsEnd     bool
	processingDone chan struct{}
}

func NewServer(root context.Context, readers Readers, settings Settings, observer Observer) (*Server, error) {
	if root == nil || readers.Instruments == nil || readers.Tickers == nil || readers.MarketStats == nil || readers.Klines == nil ||
		settings.SnapshotTimeout <= 0 || settings.KlineTimeout <= 0 || settings.WriteGrace <= 0 || settings.MaxSnapshots <= 0 || settings.MaxKlines <= 0 ||
		settings.MaxRequestBytes <= 0 || settings.MaxResponseBytes <= 0 || settings.MaxHeaderBytes <= 0 || settings.MaxHeaderBytes > math.MaxInt32 ||
		settings.ReadHeaderTimeout <= 0 || settings.IdleTimeout <= 0 || settings.SnapshotTimeout > time.Duration(math.MaxInt64)-settings.WriteGrace || settings.KlineTimeout > time.Duration(math.MaxInt64)-settings.WriteGrace {
		return nil, application.ErrInvalidParameter
	}
	s := &Server{settings: settings, snapshots: make(chan struct{}, settings.MaxSnapshots), klines: make(chan struct{}, settings.MaxKlines), observer: observer}
	s.grpc = grpc.NewServer(grpc.MaxRecvMsgSize(settings.MaxRequestBytes), grpc.MaxSendMsgSize(settings.MaxResponseBytes), grpc.UnaryInterceptor(s.intercept), grpc.StatsHandler(rpcStats{}))
	pb.RegisterMarketDataServiceServer(s.grpc, &handlers{readers: readers, maximum: settings.MaxResponseBytes})
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	s.http = &http.Server{Handler: http.HandlerFunc(s.serveHTTP), Protocols: protocols, MaxHeaderBytes: settings.MaxHeaderBytes,
		ReadHeaderTimeout: settings.ReadHeaderTimeout, IdleTimeout: settings.IdleTimeout,
		HTTP2: &http.HTTP2Config{WriteByteTimeout: settings.WriteGrace}, BaseContext: func(net.Listener) context.Context { return root }}
	return s, nil
}

func (s *Server) Serve(listener net.Listener) error { return s.http.Serve(listener) }

func (s *Server) StopAdmission() {
	s.admission.Lock()
	defer s.admission.Unlock()
	s.stopped.Store(true)
}

// ServeHTTP transports cannot use grpc.GracefulStop (Drain is unsupported).
// net/http owns connections, trailers, HTTP/2 draining, and forced closure.
func (s *Server) Shutdown(ctx context.Context) error {
	s.StopAdmission()
	err := s.http.Shutdown(ctx)
	if err != nil {
		_ = s.http.Close()
	}
	finished := make(chan struct{})
	go func() {
		s.grpc.Stop()
		s.owned.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
}

func (s *Server) Close() error {
	s.StopAdmission()
	err := s.http.Close()
	s.grpc.Stop()
	return err
}

func methodName(path string) string {
	switch path {
	case pb.MarketDataService_ListInstruments_FullMethodName:
		return "ListInstruments"
	case pb.MarketDataService_ListTickers_FullMethodName:
		return "ListTickers"
	case pb.MarketDataService_ListMarketStats_FullMethodName:
		return "ListMarketStats"
	case pb.MarketDataService_GetKlines_FullMethodName:
		return "GetKlines"
	default:
		return "unknown"
	}
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.admission.Lock()
	if s.stopped.Load() {
		s.admission.Unlock()
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "14")
		w.Header().Set("Grpc-Message", "Service is stopping")
		return
	}
	s.owned.Add(1)
	s.admission.Unlock()
	started := time.Now()
	name := methodName(r.URL.Path)
	lifetime, slots := s.settings.SnapshotTimeout, s.snapshots
	if name == "GetKlines" {
		lifetime, slots = s.settings.KlineTimeout, s.klines
	}
	workDeadline, writeDeadline := started.Add(lifetime), started.Add(lifetime+s.settings.WriteGrace)
	if duration, ok := clientTimeout(r.Header.Get("Grpc-Timeout")); ok {
		clientDeadline := started.Add(duration)
		if clientDeadline.Before(workDeadline) {
			workDeadline = clientDeadline
		}
		if clientDeadline.Before(writeDeadline) {
			writeDeadline = clientDeadline
		}
	}
	if deadline, ok := r.Context().Deadline(); ok {
		if deadline.Before(workDeadline) {
			workDeadline = deadline
		}
		if deadline.Before(writeDeadline) {
			writeDeadline = deadline
		}
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(writeDeadline); err != nil {
		s.owned.Done()
		http.Error(w, "HTTP/2 required", http.StatusHTTPVersionNotSupported)
		return
	}
	// This pinned HTTP/2 hook signals normal END_STREAM as well as abort. Capture
	// it before returning: ResponseWriter cannot be used after Handler returns.
	closed := w.(http.CloseNotifier).CloseNotify() //nolint:staticcheck // Request.Context closes before final trailers; this pinned hook waits for stream closure.
	state := &call{deadline: workDeadline, event: Event{Method: name}, processingDone: make(chan struct{})}
	acquired := false
	if headerBytes(r) > s.settings.MaxHeaderBytes {
		state.reject = application.ErrRequestTooLarge
	}
	if state.reject != nil {
		// The decoded header budget is independent of net/http parser overhead.
	} else if s.stopped.Load() {
		state.reject = context.Canceled
	} else {
		select {
		case slots <- struct{}{}:
			acquired = true
		default:
			state.reject = application.ErrServiceOverloaded
		}
	}
	finish := func(Event) {}
	if s.observer != nil {
		finish = s.observer(r.Context(), name)
	}
	response := &responseWriter{ResponseWriter: w}
	ctx := context.WithValue(r.Context(), callKey{}, state)
	// Root/client cancellation aborts writes while the handler is running. Stop
	// and join this callback before the writer can be recycled by net/http.
	canceled := make(chan struct{})
	stop := context.AfterFunc(r.Context(), func() {
		_ = controller.SetReadDeadline(time.Now())
		_ = controller.SetWriteDeadline(time.Now())
		close(canceled)
	})
	if state.reject != nil {
		recordError(state, state.reject)
		writeStatus(response, mapError(state.reject))
	} else if name == "unknown" {
		// Native method lookup needs no message. Never hand its background
		// reader the live request body, even for an unknown method.
		bounded := r.WithContext(ctx)
		bounded.Body = http.NoBody
		s.grpc.ServeHTTP(response, bounded)
	} else {
		readErr := controller.SetReadDeadline(workDeadline)
		var message []byte
		if readErr == nil {
			message, readErr = readUnary(r.Body, s.settings.MaxRequestBytes)
		}
		if readErr != nil {
			failure := readFailure(r.Context(), workDeadline, readErr)
			recordNativeError(state, failure)
			writeStatus(response, failure)
		} else {
			_ = controller.SetReadDeadline(time.Time{})
			bounded := r.WithContext(ctx)
			bounded.Body = io.NopCloser(bytes.NewReader(message))
			s.grpc.ServeHTTP(response, bounded)
		}
	}
	if !stop() {
		<-canceled
	}
	flushErr := controller.Flush()
	if response.err != nil {
		flushErr = response.err
	}
	state.mu.Lock()
	state.closed = true
	expectsEnd := state.expectsEnd
	event := state.event
	state.mu.Unlock()
	// Unknown methods can return native status without interceptor or stats.End.
	if value := response.Header().Get("Grpc-Status"); value != "" {
		if code, err := strconv.ParseUint(value, 10, 32); err == nil && codes.Code(code) != codes.OK && event.Code == codes.OK {
			event.Code, event.Reason = codes.Code(code), "transport_error"
			event.Error, event.TransportFailure = status.Error(event.Code, "RPC transport failed"), true
		}
	}
	if event.Code == codes.OK && (response.status >= 400 || !expectsEnd) {
		event.Code, event.Reason = httpFailureCode(response.status), "transport_error"
		event.Error, event.TransportFailure = status.Error(event.Code, "RPC transport failed"), true
	}
	if flushErr != nil {
		event.TransportFailure, event.Error = true, flushErr
		event.Code, event.Reason = codes.Unavailable, "transport_error"
		if errors.Is(flushErr, context.DeadlineExceeded) || errors.Is(flushErr, http.ErrHandlerTimeout) || errors.Is(flushErr, os.ErrDeadlineExceeded) {
			event.Code, event.Reason, event.Error = codes.DeadlineExceeded, "request_timeout", context.DeadlineExceeded
		}
	}
	// ServeHTTP can finish cancellation before the application goroutine has
	// recorded stats.End. The request's observed cause still determines outcome.
	if r.Context().Err() != nil {
		if errors.Is(r.Context().Err(), context.Canceled) && time.Now().Before(writeDeadline) {
			event.Code, event.Reason, event.Error = codes.Canceled, "request_canceled", context.Canceled
		} else {
			event.Code, event.Reason, event.Error = codes.DeadlineExceeded, "request_timeout", context.DeadlineExceeded
		}
		event.TransportFailure = true
	}
	// Keep the finite write deadline armed for the final trailers. This owner
	// holds capacity until both stream closure and native RPC processing finish.
	go func() {
		defer s.owned.Done()
		<-closed
		if expectsEnd {
			<-state.processingDone
		}
		state.mu.Lock()
		latest := state.event
		state.mu.Unlock()
		if event.TransportFailure {
			latest.Code, latest.Reason, latest.Error = event.Code, event.Reason, event.Error
			latest.TransportFailure = true
		}
		event = latest
		if acquired {
			<-slots
		}
		event.Duration = time.Since(started)
		finish(event)
	}()
}

func (s *Server) intercept(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response any, err error) {
	state := ctx.Value(callKey{}).(*call)
	work, cancel := context.WithDeadline(ctx, state.deadline)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = application.ErrInternal
			state.mu.Lock()
			state.event.Panic = true
			state.mu.Unlock()
		}
		if err == nil {
			err = work.Err()
		}
		state.mu.Lock()
		state.event.Error = err
		mapped := mapError(err)
		state.event.Code = status.Code(mapped)
		if mapped != nil {
			for _, detail := range status.Convert(mapped).Details() {
				if detail, ok := detail.(*pb.ErrorDetail); ok {
					state.event.Reason = detail.Reason
				}
			}
		}
		state.mu.Unlock()
		if mapped != nil {
			response = nil
		}
		err = mapped
	}()
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		return nil, context.Canceled
	}
	if err := work.Err(); err != nil {
		return nil, err
	}
	if state.reject != nil {
		return nil, state.reject
	}
	response, err = handler(work, request)
	if err == nil && proto.Size(response.(proto.Message)) > s.settings.MaxResponseBytes {
		return nil, errResponseTooLarge
	}
	return response, err
}

func clientTimeout(value string) (time.Duration, bool) {
	if len(value) < 2 || len(value) > 9 {
		return 0, false
	}
	unit := time.Duration(0)
	switch value[len(value)-1] {
	case 'H':
		unit = time.Hour
	case 'M':
		unit = time.Minute
	case 'S':
		unit = time.Second
	case 'm':
		unit = time.Millisecond
	case 'u':
		unit = time.Microsecond
	case 'n':
		unit = time.Nanosecond
	default:
		return 0, false
	}
	digits := value[:len(value)-1]
	if strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, false
	}
	if n > uint64(math.MaxInt64)/uint64(unit) {
		return time.Duration(math.MaxInt64), true
	}
	return time.Duration(n) * unit, true
}

type responseWriter struct {
	http.ResponseWriter
	err    error
	status int
}

func (w *responseWriter) WriteHeader(code int) {
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(body)
	if err != nil {
		w.err = err
	}
	return n, err
}

func (w *responseWriter) Flush() {
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		w.err = err
	}
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type rpcStats struct{}

func (rpcStats) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context   { return ctx }
func (rpcStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (rpcStats) HandleConn(ctx context.Context, event stats.ConnStats) {
	state, ok := ctx.Value(callKey{}).(*call)
	if !ok {
		return
	}
	if _, ok := event.(*stats.ConnBegin); ok {
		// ServeHTTP starts this hook before scheduling its single RPC. Known
		// unary methods always emit End, after application and serialization.
		state.mu.Lock()
		state.expectsEnd = state.event.Method != "unknown"
		state.mu.Unlock()
	}
}

func (rpcStats) HandleRPC(ctx context.Context, event stats.RPCStats) {
	state, ok := ctx.Value(callKey{}).(*call)
	if !ok {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	switch event := event.(type) {
	case *stats.InPayload:
		state.event.RequestBytes += event.Length
	case *stats.OutPayload:
		state.event.ResponseBytes += event.Length
	case *stats.End:
		close(state.processingDone)
		if event.Error != nil && state.event.Error == nil {
			state.event.Code, state.event.Error, state.event.TransportFailure = status.Code(event.Error), event.Error, true
			state.event.Reason = "transport_error"
		}
	}
}

// Match HTTP/2 header-list accounting, including pseudo headers and field overhead.
func headerBytes(r *http.Request) int {
	size := len(":method") + len(r.Method) + 32 + len(":scheme") + len(r.URL.Scheme) + 32 + len(":authority") + len(r.Host) + 32 + len(":path") + len(r.URL.RequestURI()) + 32
	if r.URL.Scheme == "" {
		size += len("http")
	}
	for name, values := range r.Header {
		for _, value := range values {
			size += len(name) + len(value) + 32
		}
	}
	return size
}

func readFailure(ctx context.Context, deadline time.Time, err error) error {
	if ctx.Err() != nil && time.Now().Before(deadline) {
		return mapError(ctx.Err())
	}
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return mapError(context.DeadlineExceeded)
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Internal, "Invalid request frame")
}

func recordError(state *call, err error) {
	mapped := mapError(err)
	state.mu.Lock()
	defer state.mu.Unlock()
	state.event.Error = err
	state.event.Code = status.Code(mapped)
	for _, detail := range status.Convert(mapped).Details() {
		if detail, ok := detail.(*pb.ErrorDetail); ok {
			state.event.Reason = detail.Reason
		}
	}
}

func recordNativeError(state *call, err error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.event.Error, state.event.Code, state.event.TransportFailure = err, status.Code(err), true
	state.event.Reason = "transport_error"
	for _, detail := range status.Convert(err).Details() {
		if detail, ok := detail.(*pb.ErrorDetail); ok {
			state.event.Reason = detail.Reason
		}
	}
}

// Match the standard gRPC fallback for HTTP responses without grpc-status.
func httpFailureCode(code int) codes.Code {
	switch code {
	case http.StatusBadRequest:
		return codes.Internal
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.Unimplemented
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return codes.Unavailable
	default:
		return codes.Unknown
	}
}
