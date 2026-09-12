// Package bybit keeps Bybit SDK details inside the exchange adapter.
package bybit

import (
	"context"
	"fmt"
	"net/url"

	sdk "github.com/bybit-exchange/bybit.go.api"

	"market-data/internal/application"
	"market-data/internal/infrastructure/exchange/upstream"
)

type Client struct{ sdk *sdk.Client }

func NewClient(baseURL string, transport *upstream.Transport) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.User != nil || transport == nil {
		return nil, fmt.Errorf("invalid Bybit client settings")
	}
	client := sdk.NewBybitHttpClient("", "", sdk.WithBaseURL(baseURL))
	client.HTTPClient = upstream.HTTPClient(transport)
	return &Client{sdk: client}, nil
}

func (c *Client) Fetch(ctx context.Context, path string, parameters url.Values) (upstream.Response, error) {
	if path != "/v5/market/instruments-info" && path != "/v5/market/tickers" && path != "/v5/market/kline" {
		return upstream.Response{}, application.ErrUnsupportedOperation
	}
	values := make(map[string]interface{}, len(parameters))
	for key, value := range parameters {
		if len(value) != 1 || value[0] == "" {
			return upstream.Response{}, application.ErrInvalidParameter
		}
		values[key] = value[0]
	}
	return upstream.Execute(ctx, func(ctx context.Context) error {
		request := c.sdk.NewUtaBybitServiceWithParams(values)
		var err error
		switch path {
		case "/v5/market/instruments-info":
			_, err = request.GetInstrumentInfo(ctx)
		case "/v5/market/tickers":
			_, err = request.GetMarketTickers(ctx)
		case "/v5/market/kline":
			_, err = request.GetMarketKline(ctx)
		default:
			return application.ErrUnsupportedOperation
		}
		return err
	})
}
