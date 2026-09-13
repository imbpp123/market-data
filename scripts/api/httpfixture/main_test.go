package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"market-data/internal/application/kline"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFixedHTTPResponses(t *testing.T) {
	cases := []struct {
		name, path string
		count      int
	}{
		{"small instruments", "/api/v1/instruments", 1},
		{"full instruments", "/api/v1/instruments?symbol=full", 20000},
		{"full tickers", "/api/v1/tickers?symbol=full", 20000},
		{"full stats", "/api/v1/market-stats?symbol=full", 20000},
		{"one candle", "/api/v1/klines?exchange=binance&market=spot&symbol=S0000USDT&interval=1m&from=2026-09-11T19:20:00Z&to=2026-09-11T19:21:00Z", 1},
	}
	target := handler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			target.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))
			require.Equal(t, http.StatusOK, response.Code)
			var body struct {
				Data []map[string]json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Len(t, body.Data, tc.count)
			assert.Contains(t, response.Body.String(), fixtureDecimal)
			if tc.name == "full stats" {
				assert.Equal(t, "9007199254740993", string(body.Data[0]["trade_count"]))
			}
		})
	}
}

func TestFixtureRangeBounds(t *testing.T) {
	cases := []struct {
		name    string
		minutes int
		valid   bool
	}{
		{"empty", 0, true}, {"maximum", 1000, true}, {"reversed", -1, false}, {"oversized", 1001, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(fixtureEpoch, 0)
			err := klineFixture(nil).Validate(kline.Query{From: start, To: start.Add(time.Duration(tc.minutes) * time.Minute)})
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestConnectionByteCounters(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
	counts := &counters{}
	connection := countedConn{Conn: left, counts: counts}
	done := make(chan error, 1)
	go func() { _, err := connection.Write([]byte("payload")); done <- err }()
	received := make([]byte, 7)
	_, err := io.ReadFull(right, received)
	require.NoError(t, err)
	require.NoError(t, <-done)
	assert.Equal(t, []byte("payload"), received)
	assert.Equal(t, int64(7), counts.written.Load())
	go func() { _, err := io.Copy(right, bytes.NewBufferString("reply")); done <- err }()
	received = make([]byte, 5)
	_, err = io.ReadFull(connection, received)
	require.NoError(t, err)
	require.NoError(t, <-done)
	assert.Equal(t, []byte("reply"), received)
	assert.Equal(t, int64(5), counts.read.Load())
}
