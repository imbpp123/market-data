package upstream

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/andybalholm/brotli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"market-data/internal/application"
	"market-data/internal/config"
)

func TestBodyLimitAppliesAfterDecompression(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "br"} {
		t.Run(encoding, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var compressed bytes.Buffer
				var writer io.WriteCloser
				switch encoding {
				case "gzip":
					writer = gzip.NewWriter(&compressed)
				case "deflate":
					writer = zlib.NewWriter(&compressed)
				case "br":
					writer = brotli.NewWriter(&compressed)
				}

				body := `{"retCode":0,"result":{"data":"` + strings.Repeat("x", 1024) + `"}}`
				_, err := io.WriteString(writer, body)
				require.NoError(t, err)
				require.NoError(t, writer.Close())

				cfg := config.Defaults()
				cfg.HTTPClient.MaxResponseBytes = 128
				transportBase := roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Encoding": {encoding}}, Body: io.NopCloser(bytes.NewReader(compressed.Bytes()))}, nil
				})
				c, transport := setup(t, cfg, Bybit, transportBase)

				_, err = send(begin(t, c, Bybit, Tickers), transport, "/v5/market/tickers?category=spot")

				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
			})
		})
	}
}

func TestExactBodyLimitAndCompressedSuccess(t *testing.T) {
	cases := []struct {
		name     string
		encoding string
		compress func(io.Writer) io.WriteCloser
	}{
		{name: "plain"},
		{name: "gzip", encoding: "gzip", compress: func(w io.Writer) io.WriteCloser { return gzip.NewWriter(w) }},
		{name: "deflate", encoding: "deflate", compress: func(w io.Writer) io.WriteCloser { return zlib.NewWriter(w) }},
		{name: "brotli", encoding: "br", compress: func(w io.Writer) io.WriteCloser { return brotli.NewWriter(w) }},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				body := `{"retCode":0,"result":{"number":9007199254740993}}`
				cfg := config.Defaults()
				cfg.HTTPClient.MaxResponseBytes = len(body)
				encoded := []byte(body)
				if tt.compress != nil {
					var buffer bytes.Buffer
					writer := tt.compress(&buffer)
					_, err := io.WriteString(writer, body)
					require.NoError(t, err)
					require.NoError(t, writer.Close())
					encoded = buffer.Bytes()
				}

				base := roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Encoding": {tt.encoding}},
						Body:       io.NopCloser(bytes.NewReader(encoded)),
					}, nil
				})
				c, transport := setup(t, cfg, Bybit, base)

				actual, err := send(begin(t, c, Bybit, Tickers), transport, "/v5/market/tickers?category=spot")

				require.NoError(t, err)
				assert.Equal(t, body, string(actual))
			})
		})
	}
}
