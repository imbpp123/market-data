// Package upstream bounds and accounts for exchange HTTP attempts.
package upstream

import (
	"fmt"
	"net/http"
	"strconv"

	"market-data/internal/application"
)

type Scope string

const (
	BinanceSpot   Scope = "binance_spot"
	BinanceLinear Scope = "binance_linear"
	Bybit         Scope = "bybit"
)

type Operation int

const (
	Tickers Operation = iota
	Klines
	Instruments
	MarketStats
	operationCount
)

type cost struct {
	operation Operation
	weight    int
	funding   bool
}

func resolveCost(scope Scope, request *http.Request, pageMaximum int) (cost, error) {
	invalid := fmt.Errorf("unknown upstream request cost: %w", application.ErrUnsupportedOperation)
	if request.Method != http.MethodGet || request.Body != nil && request.Body != http.NoBody {
		return cost{}, invalid
	}
	q, err := parseQuery(request)
	if err != nil {
		return cost{}, err
	}
	path := request.URL.Path
	c := cost{}
	switch scope {
	case BinanceSpot:
		switch path {
		case "/api/v3/exchangeInfo":
			c = cost{operation: Instruments, weight: 20}
		case "/api/v3/ticker/price", "/api/v3/ticker/bookTicker":
			c = cost{operation: Tickers, weight: 4}
		case "/api/v3/ticker/24hr":
			if q.Get("type") != "" && q.Get("type") != "FULL" {
				return cost{}, invalid
			}
			c = cost{operation: MarketStats, weight: 80}
		case "/api/v3/klines":
			c = cost{operation: Klines, weight: 2}
		default:
			return cost{}, invalid
		}
	case BinanceLinear:
		switch path {
		case "/fapi/v1/exchangeInfo":
			c = cost{operation: Instruments, weight: 1}
		case "/fapi/v1/fundingInfo":
			c = cost{operation: Instruments, funding: true}
		case "/fapi/v2/ticker/price":
			c = cost{operation: Tickers, weight: 2}
		case "/fapi/v1/ticker/bookTicker":
			c = cost{operation: Tickers, weight: 5}
		case "/fapi/v1/premiumIndex":
			c = cost{operation: Tickers, weight: 10}
		case "/fapi/v1/ticker/24hr":
			c = cost{operation: MarketStats, weight: 40}
		case "/fapi/v1/klines":
			c = cost{operation: Klines}
		default:
			return cost{}, invalid
		}
	case Bybit:
		if q.Get("category") != "spot" && q.Get("category") != "linear" {
			return cost{}, invalid
		}
		switch path {
		case "/v5/market/instruments-info":
			c.operation = Instruments
		case "/v5/market/tickers":
			c.operation = Tickers
		case "/v5/market/kline":
			c.operation = Klines
		default:
			return cost{}, invalid
		}
	default:
		return cost{}, invalid
	}
	if c.operation == Klines {
		limit, err := strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > pageMaximum {
			return cost{}, invalid
		}
		if scope == BinanceLinear {
			switch {
			case limit < 100:
				c.weight = 1
			case limit < 500:
				c.weight = 2
			case limit <= 1000:
				c.weight = 5
			default:
				c.weight = 10
			}
		}
	} else if scope != Bybit && (q.Has("symbol") || q.Has("symbols")) {
		// Only the bulk snapshot paths are part of the v1 cost contract.
		return cost{}, invalid
	}
	return c, nil
}
