package grpctransport

import (
	"encoding/base64"
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Read one unary frame and EOF before grpc-go starts its asynchronous reader.
// The lookahead consumes at most one extra byte, never an unbounded DATA tail.
// Protobuf fields and compression validation still belong to the runtime.
func readUnary(body io.Reader, maximum int) ([]byte, error) {
	var prefix [5]byte
	if _, err := io.ReadFull(body, prefix[:]); err != nil {
		return nil, err
	}
	length := uint64(binary.BigEndian.Uint32(prefix[1:]))
	if length > uint64(maximum) || length > uint64(math.MaxInt-5) {
		return nil, status.Error(codes.ResourceExhausted, "Request is too large")
	}
	message := make([]byte, 5+int(length))
	copy(message, prefix[:])
	if _, err := io.ReadFull(body, message[5:]); err != nil {
		return nil, err
	}
	var extra [1]byte
	n, err := io.ReadFull(body, extra[:])
	if n != 0 {
		return nil, status.Error(codes.InvalidArgument, "One request message is required")
	}
	if err != io.EOF {
		return nil, err
	}
	return message, nil
}

// A gRPC status response can reject admission without reading the body.
func writeStatus(w http.ResponseWriter, err error) {
	result := status.Convert(err)
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Add("Trailer", "Grpc-Status")
	w.Header().Add("Trailer", "Grpc-Message")
	w.Header().Add("Trailer", "Grpc-Status-Details-Bin")
	w.Header().Set("Grpc-Status", strconv.Itoa(int(result.Code())))
	w.Header().Set("Grpc-Message", result.Message())
	if len(result.Proto().Details) != 0 {
		encoded, encodeErr := proto.Marshal(result.Proto())
		if encodeErr == nil {
			w.Header().Set("Grpc-Status-Details-Bin", base64.RawStdEncoding.EncodeToString(encoded))
		}
	}
}
