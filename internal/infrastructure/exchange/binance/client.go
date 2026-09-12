// Package binance keeps Binance SDK details inside the exchange adapter.
package binance

import (
	"context"
	"fmt"
	"net/url"

	"github.com/binance/binance-connector-go/common/v2/common"

	"market-data/internal/infrastructure/exchange/upstream"
)

// Client keeps the raw response boundary shared by the two Binance SDK adapters.
type Client interface {
	Fetch(context.Context, string, url.Values) (upstream.Response, error)
}

func NewClient(scope upstream.Scope, baseURL string, transport *upstream.Transport) (Client, error) {
	cfg, err := clientConfiguration(baseURL, transport)
	if err != nil {
		return nil, err
	}

	switch scope {
	case upstream.BinanceSpot:
		return &spotClient{config: cfg}, nil
	case upstream.BinanceLinear:
		return &linearClient{config: cfg}, nil
	default:
		return nil, fmt.Errorf("invalid Binance client scope")
	}
}

func clientConfiguration(baseURL string, transport *upstream.Transport) (*common.ConfigurationRestAPI, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.User != nil || transport == nil {
		return nil, fmt.Errorf("invalid Binance client settings")
	}

	cfg := common.NewConfigurationRestAPI()
	cfg.BasePath = baseURL
	cfg.HTTPSAgent = transport
	cfg.Retries = 0
	// Admission and operation deadlines are separate from HTTP attempt timeout.
	cfg.Timeout = 0
	cfg.Compression = false
	return cfg, nil
}
