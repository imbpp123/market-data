package bybit

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"market-data/internal/application"
	"market-data/internal/application/kline"
	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/normalization"
)

var _ kline.Provider = (*klineProvider)(nil)

var klineIntervals = [...]struct {
	interval domain.Timeframe
	value    string
}{
	{domain.Timeframe1m, "1"},
	{domain.Timeframe3m, "3"},
	{domain.Timeframe5m, "5"},
	{domain.Timeframe15m, "15"},
	{domain.Timeframe30m, "30"},
	{domain.Timeframe1h, "60"},
	{domain.Timeframe2h, "120"},
	{domain.Timeframe4h, "240"},
	{domain.Timeframe6h, "360"},
	{domain.Timeframe12h, "720"},
	{domain.Timeframe1d, "D"},
	{domain.Timeframe1w, "W"},
	{domain.Timeframe1M, "M"},
}

type klineProvider struct{ client *Client }

func NewKlineProvider(client *Client) *klineProvider {
	return &klineProvider{client: client}
}

func (*klineProvider) Exchange() domain.Exchange { return domain.ExchangeBybit }

func (*klineProvider) SupportedTimeframes(market domain.Market) ([]domain.Timeframe, error) {
	if !market.Valid() {
		return nil, application.ErrInvalidFilter
	}

	result := make([]domain.Timeframe, 0, len(klineIntervals))
	for _, entry := range klineIntervals {
		result = append(result, entry.interval)
	}

	return result, nil
}

func klineInterval(interval domain.Timeframe) string {
	for _, entry := range klineIntervals {
		if entry.interval == interval {
			return entry.value
		}
	}

	return ""
}

// GetKlines executes one planned page in the caller's bounded fill context.
func (p *klineProvider) GetKlines(ctx context.Context, request kline.Request) ([]kline.Stored, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if !request.Market.Valid() || request.Exchange != p.Exchange() {
		return nil, application.ErrInvalidFilter
	}

	interval := klineInterval(request.Interval)
	if interval == "" {
		return nil, application.ErrInvalidInterval
	}

	if err := normalization.KlineRequest(request, p.Exchange()); err != nil {
		return nil, err
	}

	if p.client == nil {
		return nil, application.ErrUnsupportedOperation
	}

	response, err := p.client.Fetch(ctx, "/v5/market/kline", url.Values{
		"category": {string(request.Market)}, "symbol": {request.Symbol},
		"interval": {interval}, "limit": {strconv.Itoa(request.Limit)},
		"start": {strconv.FormatInt(request.From.UnixMilli(), 10)},
		"end":   {strconv.FormatInt(request.To.UnixMilli()-1, 10)},
	})
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Code   *int64 `json:"retCode"`
		Result struct {
			Category string               `json:"category"`
			Symbol   string               `json:"symbol"`
			List     *[][]json.RawMessage `json:"list"`
		} `json:"result"`
	}
	if json.Unmarshal(response.Body, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || envelope.Result.Category != string(request.Market) || envelope.Result.Symbol != request.Symbol || envelope.Result.List == nil {
		return nil, normalization.Invalid("candle envelope")
	}

	return normalization.Klines(ctx, request, *envelope.Result.List, response)
}
