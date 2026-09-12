package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	sdk "github.com/binance/binance-connector-go/clients/spot/src/restapi"
	"github.com/binance/binance-connector-go/common/v2/common"

	"market-data/internal/infrastructure/exchange/upstream"
)

type spotClient struct{ config *common.ConfigurationRestAPI }

func (c *spotClient) Fetch(ctx context.Context, path string, parameters url.Values) (upstream.Response, error) {
	return upstream.Execute(ctx, func(ctx context.Context) error {
		_, err := sdk.SendRequest[json.RawMessage](ctx, c.config.BasePath+path, http.MethodGet, parameters, nil, c.config, false)
		return err
	})
}
