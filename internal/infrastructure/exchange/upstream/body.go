package upstream

import (
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"

	"market-data/internal/application"
)

// Decode before the size check, so SDK decompression cannot expand a small
// compressed response into unbounded memory after admission has completed.
func responseBody(response *http.Response) (io.ReadCloser, error) {
	raw := response.Body
	var reader io.Reader
	var decoder io.Closer
	switch strings.ToLower(response.Header.Get("Content-Encoding")) {
	case "", "identity":
		return raw, nil
	case "gzip":
		gzipReader, err := gzip.NewReader(raw)
		if err != nil {
			return nil, application.ErrInvalidUpstreamData
		}
		reader, decoder = gzipReader, gzipReader
	case "deflate":
		deflateReader, err := zlib.NewReader(raw)
		if err != nil {
			return nil, application.ErrInvalidUpstreamData
		}
		reader, decoder = deflateReader, deflateReader
	case "br":
		reader = brotli.NewReader(raw)
	default:
		return nil, application.ErrInvalidUpstreamData
	}
	response.Header.Del("Content-Encoding")
	response.Header.Del("Content-Length")
	response.Uncompressed = true
	return decodedBody{Reader: reader, decoder: decoder, raw: raw}, nil
}

type decodedBody struct {
	io.Reader
	decoder io.Closer
	raw     io.Closer
}

func (b decodedBody) Close() error {
	if b.decoder != nil {
		_ = b.decoder.Close()
	}
	return b.raw.Close()
}
