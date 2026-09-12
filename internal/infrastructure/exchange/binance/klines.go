package binance

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
	interval     domain.Timeframe
	spot, linear string
}{
	{domain.Timeframe1s, "1s", ""},
	{domain.Timeframe1m, "1m", "1m"},
	{domain.Timeframe3m, "3m", "3m"},
	{domain.Timeframe5m, "5m", "5m"},
	{domain.Timeframe15m, "15m", "15m"},
	{domain.Timeframe30m, "30m", "30m"},
	{domain.Timeframe1h, "1h", "1h"},
	{domain.Timeframe2h, "2h", "2h"},
	{domain.Timeframe4h, "4h", "4h"},
	{domain.Timeframe6h, "6h", "6h"},
	{domain.Timeframe8h, "8h", "8h"},
	{domain.Timeframe12h, "12h", "12h"},
	{domain.Timeframe1d, "1d", "1d"},
	{domain.Timeframe3d, "3d", "3d"},
	{domain.Timeframe1w, "1w", "1w"},
	{domain.Timeframe1M, "1M", "1M"},
}

type klineProvider struct {
	clients map[domain.Market]Client
}

func NewKlineProvider(spot, linear Client) *klineProvider {
	return &klineProvider{clients: map[domain.Market]Client{domain.MarketSpot: spot, domain.MarketLinear: linear}}
}

func (*klineProvider) Exchange() domain.Exchange { return domain.ExchangeBinance }

func (*klineProvider) SupportedTimeframes(market domain.Market) ([]domain.Timeframe, error) {
	if !market.Valid() {
		return nil, application.ErrInvalidFilter
	}

	result := make([]domain.Timeframe, 0, len(klineIntervals))
	for _, entry := range klineIntervals {
		if klineInterval(market, entry.interval) != "" {
			result = append(result, entry.interval)
		}
	}

	return result, nil
}

func klineInterval(market domain.Market, interval domain.Timeframe) string {
	for _, entry := range klineIntervals {
		if entry.interval == interval {
			if market == domain.MarketSpot {
				return entry.spot
			}

			return entry.linear
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

	interval := klineInterval(request.Market, request.Interval)
	if interval == "" {
		return nil, application.ErrInvalidInterval
	}

	if err := normalization.KlineRequest(request, p.Exchange()); err != nil {
		return nil, err
	}

	client := p.clients[request.Market]
	if client == nil {
		return nil, application.ErrUnsupportedOperation
	}

	path := "/api/v3/klines"
	if request.Market == domain.MarketLinear {
		path = "/fapi/v1/klines"
	}

	parameters := url.Values{
		"symbol": {request.Symbol}, "interval": {interval}, "limit": {strconv.Itoa(request.Limit)},
		"startTime": {strconv.FormatInt(request.From.UnixMilli(), 10)},
		"endTime":   {strconv.FormatInt(request.To.UnixMilli()-1, 10)},
	}
	response, err := client.Fetch(ctx, path, parameters)
	if err != nil {
		return nil, err
	}

	var rows *[][]json.RawMessage
	if json.Unmarshal(response.Body, &rows) != nil || rows == nil {
		return nil, normalization.Invalid("candle envelope")
	}

	return normalization.Klines(ctx, request, *rows, response)
}
