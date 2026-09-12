package bybit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"

	"market-data/internal/domain"
	"market-data/internal/infrastructure/exchange/normalization"
)

type instrumentRow struct {
	Symbol          string      `json:"symbol"`
	BaseCoin        string      `json:"baseCoin"`
	QuoteCoin       string      `json:"quoteCoin"`
	Status          string      `json:"status"`
	ContractType    string      `json:"contractType"`
	FundingInterval json.Number `json:"fundingInterval"`
	DeliveryTime    string      `json:"deliveryTime"`
	PriceFilter     struct {
		TickSize string `json:"tickSize"`
	} `json:"priceFilter"`
	LotSizeFilter struct {
		QtyStep          string `json:"qtyStep"`
		BasePrecision    string `json:"basePrecision"`
		MinOrderQty      string `json:"minOrderQty"`
		MaxOrderQty      string `json:"maxOrderQty"`
		MaxLimitOrderQty string `json:"maxLimitOrderQty"`
		MinNotionalValue string `json:"minNotionalValue"`
		MinOrderAmt      string `json:"minOrderAmt"`
	} `json:"lotSizeFilter"`
}

type instrumentPage struct {
	Category string           `json:"category"`
	List     *[]instrumentRow `json:"list"`
	Cursor   string           `json:"nextPageCursor"`
}

func fetchInstrumentPage(ctx context.Context, client *Client, parameters url.Values) (instrumentPage, error) {
	response, err := client.Fetch(ctx, "/v5/market/instruments-info", parameters)
	if err != nil {
		return instrumentPage{}, err
	}

	var envelope struct {
		Code   *int           `json:"retCode"`
		Result instrumentPage `json:"result"`
	}

	if json.Unmarshal(response.Body, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || envelope.Result.Category != parameters.Get("category") || envelope.Result.List == nil {
		return instrumentPage{}, normalization.Invalid("instrument envelope")
	}

	return envelope.Result, nil
}

func normalizeInstrumentPrice(source instrumentRow, market domain.Market) (domain.Instrument, error) {
	row := domain.Instrument{Exchange: domain.ExchangeBybit, Market: market, Symbol: source.Symbol, BaseAsset: source.BaseCoin, QuoteAsset: source.QuoteCoin, Status: instrumentStatus(source.Status)}
	var err error
	row.PriceTick, err = normalization.Step(source.PriceFilter.TickSize)
	return row, err
}

func logUnknownStatus(logger *slog.Logger, row domain.Instrument, status string) {
	if row.Status == domain.InstrumentStatusUnknown && logger != nil {
		logger.Warn("Unknown instrument status", "exchange", row.Exchange, "market", row.Market, "symbol", row.Symbol, "status", status)
	}
}

func instrumentStatus(status string) domain.InstrumentStatus {
	switch status {
	case "PreLaunch", "PendingOpen":
		return domain.InstrumentStatusPreLaunch
	case "Trading":
		return domain.InstrumentStatusTrading
	case "Delivering":
		return domain.InstrumentStatusSettling
	case "Closed":
		return domain.InstrumentStatusClosed
	default:
		return domain.InstrumentStatusUnknown
	}
}
