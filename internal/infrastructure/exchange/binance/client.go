// Package binance keeps Binance SDK details inside the exchange adapter.
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	futuresapi "github.com/binance/binance-connector-go/clients/derivativestradingusdsfutures/src/restapi"
	spotapi "github.com/binance/binance-connector-go/clients/spot/src/restapi"
	"github.com/binance/binance-connector-go/common/v2/common"

	"market-data/internal/infrastructure/exchange/upstream"
)

// Client is the bounded raw-data foundation for feature-specific normalization.
type Client struct {
	scope  upstream.Scope
	config *common.ConfigurationRestAPI
}

func NewClient(scope upstream.Scope, baseURL string, transport *upstream.Transport) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.User != nil || transport == nil || (scope != upstream.BinanceSpot && scope != upstream.BinanceLinear) {
		return nil, fmt.Errorf("invalid Binance client settings")
	}
	cfg := common.NewConfigurationRestAPI()
	cfg.BasePath = baseURL
	cfg.HTTPSAgent = transport
	cfg.Retries = 0
	// Admission and operation deadlines are separate from HTTP attempt timeout.
	cfg.Timeout = 0
	cfg.Compression = false
	return &Client{scope: scope, config: cfg}, nil
}

func (c *Client) Fetch(ctx context.Context, path string, parameters url.Values) (upstream.Response, error) {
	return upstream.Execute(ctx, func(ctx context.Context) error {
		endpoint := c.config.BasePath + path
		// RawMessage retains exact source fields for normalization. The SDK still
		// owns request construction and processing, behind our accounted transport.
		if c.scope == upstream.BinanceSpot {
			_, err := spotapi.SendRequest[json.RawMessage](ctx, endpoint, http.MethodGet, parameters, nil, c.config, false)
			return err
		}
		_, err := futuresapi.SendRequest[json.RawMessage](ctx, endpoint, http.MethodGet, parameters, nil, c.config, false)
		return err
	})
}
