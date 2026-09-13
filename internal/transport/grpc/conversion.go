package grpctransport

import (
	"context"
	"time"

	"market-data/internal/application"
	"market-data/internal/application/ticker"
	"market-data/internal/domain"

	pb "github.com/imbpp123/market-data/api/go/marketdata/v1"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Conversion stops before appending a row that exceeds the encoded message cap.
// Repeated row fields all use one-byte tags. Only one extra row is allocated.
func convertRows[D any, P proto.Message](ctx context.Context, rows []D, maximum, size int, convert func(D) (P, error), appendRow func(P)) error {
	if size > maximum {
		return errResponseTooLarge
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		message, err := convert(row)
		if err != nil {
			return err
		}
		n := proto.Size(message)
		n += 1 + protowire.SizeVarint(uint64(n))
		if n > maximum-size {
			return errResponseTooLarge
		}
		size += n
		appendRow(message)
	}
	return ctx.Err()
}

func timestamp(value time.Time) (*timestamppb.Timestamp, error) {
	result := timestamppb.New(value)
	if value.IsZero() || result.CheckValid() != nil || result.Seconds < 0 {
		return nil, application.ErrInternal
	}
	return result, nil
}

func optionalTimestamp(value *time.Time) (*timestamppb.Timestamp, error) {
	if value == nil {
		return nil, nil
	}
	return timestamp(*value)
}

// Bound expansion before String allocates zeros for an extreme exponent.
func decimalText(value decimal.Decimal) (string, error) {
	digits, exponent := int64(value.NumDigits()), int64(value.Exponent())
	length := digits + exponent
	if exponent < 0 {
		length = digits + 1
		if -exponent >= digits {
			length = 2 - exponent
		}
	}
	if value.IsNegative() {
		length++
	}
	if length > 1024 {
		return "", application.ErrInternal
	}
	return value.String(), nil
}

func optionalDecimal(value *decimal.Decimal) (*string, error) {
	if value == nil {
		return nil, nil
	}
	text, err := decimalText(*value)
	return &text, err
}

func seconds(value *time.Duration) *int64 {
	if value == nil {
		return nil
	}
	n := int64(*value / time.Second)
	return &n
}

func instrumentMessage(row domain.Instrument) (*pb.Instrument, error) {
	result := &pb.Instrument{Exchange: string(row.Exchange), Market: string(row.Market), Symbol: row.Symbol, BaseAsset: row.BaseAsset, QuoteAsset: row.QuoteAsset, Status: string(row.Status), FundingIntervalSeconds: seconds(row.FundingInterval)}
	var err error
	if result.UpdatedAt, err = timestamp(row.UpdatedAt); err != nil {
		return nil, err
	}
	if result.DelistingTime, err = optionalTimestamp(row.DelistingTime); err != nil {
		return nil, err
	}
	if result.PriceTick, err = decimalText(row.PriceTick); err != nil {
		return nil, err
	}
	if result.QtyStep, err = decimalText(row.QtyStep); err != nil {
		return nil, err
	}
	if result.MinQty, err = optionalDecimal(row.MinQty); err != nil {
		return nil, err
	}
	if result.MaxQty, err = optionalDecimal(row.MaxQty); err != nil {
		return nil, err
	}
	if result.MinNotional, err = optionalDecimal(row.MinNotional); err != nil {
		return nil, err
	}
	return result, nil
}

func tickerMessage(row ticker.ReadModel) (*pb.Ticker, error) {
	result := &pb.Ticker{Exchange: string(row.Exchange), Market: string(row.Market), Symbol: row.Symbol, NextFundingInSeconds: seconds(row.NextFundingIn)}
	var err error
	if result.FetchedAt, err = timestamp(row.FetchedAt); err != nil {
		return nil, err
	}
	if result.LastPrice, err = decimalText(row.LastPrice); err != nil {
		return nil, err
	}
	if result.BidPrice, err = optionalDecimal(row.BidPrice); err != nil {
		return nil, err
	}
	if result.BidSize, err = optionalDecimal(row.BidSize); err != nil {
		return nil, err
	}
	if result.AskPrice, err = optionalDecimal(row.AskPrice); err != nil {
		return nil, err
	}
	if result.AskSize, err = optionalDecimal(row.AskSize); err != nil {
		return nil, err
	}
	if result.FundingRate, err = optionalDecimal(row.FundingRate); err != nil {
		return nil, err
	}
	return result, nil
}

func statsMessage(row domain.MarketStats) (*pb.MarketStats, error) {
	result := &pb.MarketStats{Exchange: string(row.Exchange), Market: string(row.Market), Symbol: row.Symbol, Window: "24h", TradeCount: row.TradeCount}
	var err error
	if result.FetchedAt, err = timestamp(row.FetchedAt); err != nil {
		return nil, err
	}
	if result.High, err = decimalText(row.High); err != nil {
		return nil, err
	}
	if result.Low, err = decimalText(row.Low); err != nil {
		return nil, err
	}
	if result.Volume, err = decimalText(row.Volume); err != nil {
		return nil, err
	}
	if result.Turnover, err = decimalText(row.Turnover); err != nil {
		return nil, err
	}
	if result.PriceChange, err = optionalDecimal(row.PriceChange); err != nil {
		return nil, err
	}
	return result, nil
}

func klineMessage(row domain.Kline) (*pb.Kline, error) {
	result := &pb.Kline{TradesCount: row.TradesCount}
	var err error
	if result.OpenTime, err = timestamp(row.OpenTime); err != nil {
		return nil, err
	}
	if result.CloseTime, err = timestamp(row.CloseTime); err != nil {
		return nil, err
	}
	if result.FetchedAt, err = timestamp(row.FetchedAt); err != nil {
		return nil, err
	}
	if result.Open, err = decimalText(row.Open); err != nil {
		return nil, err
	}
	if result.High, err = decimalText(row.High); err != nil {
		return nil, err
	}
	if result.Low, err = decimalText(row.Low); err != nil {
		return nil, err
	}
	if result.Close, err = decimalText(row.Close); err != nil {
		return nil, err
	}
	if result.Volume, err = decimalText(row.Volume); err != nil {
		return nil, err
	}
	if result.Turnover, err = decimalText(row.Turnover); err != nil {
		return nil, err
	}
	return result, nil
}
