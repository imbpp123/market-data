package main

import (
	"testing"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestReason(t *testing.T) {
	known, err := status.New(codes.InvalidArgument, "Invalid filter").WithDetails(&pb.ErrorDetail{Reason: "invalid_filter"})
	require.NoError(t, err)
	unknown := status.New(codes.Unavailable, "Unavailable").Proto()
	unknown.Details = []*anypb.Any{{TypeUrl: "type.googleapis.com/future.Unknown", Value: []byte{8, 1}}}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "known", err: known.Err(), want: "invalid_filter"},
		{name: "unknown", err: status.FromProto(unknown).Err()},
		{name: "absent", err: status.Error(codes.Unavailable, "Unavailable")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, reason(tc.err)) })
	}
}
