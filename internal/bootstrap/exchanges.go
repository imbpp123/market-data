package bootstrap

import (
	"net/http"
	"slices"

	"market-data/internal/config"
	"market-data/internal/infrastructure/exchange/binance"
	"market-data/internal/infrastructure/exchange/bybit"
	"market-data/internal/infrastructure/exchange/upstream"
)

// All clients share one controller. Creating a client never creates new budgets.
type exchangeClients struct {
	admission *upstream.Controller
	binance   map[upstream.Scope]*binance.Client
	bybit     *bybit.Client
}

func newExchangeClients(cfg config.Config, base http.RoundTripper, clock upstream.Clock, jitter upstream.Jitter, observe upstream.Observer) (*exchangeClients, error) {
	admission, err := upstream.New(cfg, clock)
	if err != nil {
		return nil, err
	}

	binanceClients, err := newBinanceExchangeClients(cfg, admission, base, jitter, observe)
	if err != nil {
		return nil, err
	}

	bybitClient, err := newBybitExchangeClient(cfg, admission, base, jitter, observe)
	if err != nil {
		return nil, err
	}

	return &exchangeClients{admission: admission, binance: binanceClients, bybit: bybitClient}, nil
}

func newBinanceExchangeClients(cfg config.Config, admission *upstream.Controller, base http.RoundTripper, jitter upstream.Jitter, observe upstream.Observer) (map[upstream.Scope]*binance.Client, error) {
	clients := make(map[upstream.Scope]*binance.Client)
	if !cfg.Exchanges.Binance.Enabled {
		return clients, nil
	}

	for _, scope := range []struct {
		id     upstream.Scope
		market string
		url    string
	}{
		{upstream.BinanceSpot, "spot", "https://api.binance.com"},
		{upstream.BinanceLinear, "linear", "https://fapi.binance.com"},
	} {
		if !slices.Contains(cfg.Exchanges.Binance.Markets, scope.market) {
			continue
		}

		transport, err := upstream.NewTransport(admission, scope.id, base, cfg, jitter, observe)
		if err != nil {
			return nil, err
		}

		client, err := binance.NewClient(scope.id, scope.url, transport)
		if err != nil {
			return nil, err
		}

		clients[scope.id] = client
	}

	return clients, nil
}

func newBybitExchangeClient(cfg config.Config, admission *upstream.Controller, base http.RoundTripper, jitter upstream.Jitter, observe upstream.Observer) (*bybit.Client, error) {
	if !cfg.Exchanges.Bybit.Enabled {
		return nil, nil
	}

	transport, err := upstream.NewTransport(admission, upstream.Bybit, base, cfg, jitter, observe)
	if err != nil {
		return nil, err
	}

	return bybit.NewClient("https://api.bybit.com", transport)
}
