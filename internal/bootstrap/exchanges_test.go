package bootstrap

import (
	"net/http"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/config"
	"market-data/internal/infrastructure/exchange/upstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type noExchangeCalls struct{ t *testing.T }

func (n noExchangeCalls) RoundTrip(*http.Request) (*http.Response, error) {
	n.t.Error("initialization must not call exchanges")
	return nil, application.ErrUpstream
}

func TestExchangeInitializationUsesEnabledScopes(t *testing.T) {
	cases := []struct {
		name              string
		binanceEnabled    bool
		binanceMarkets    []string
		bybitEnabled      bool
		wantBinanceScopes []upstream.Scope
		wantBybit         bool
	}{
		{
			name:              "all clients enabled",
			binanceEnabled:    true,
			binanceMarkets:    []string{"spot", "linear"},
			bybitEnabled:      true,
			wantBinanceScopes: []upstream.Scope{upstream.BinanceSpot, upstream.BinanceLinear},
			wantBybit:         true,
		},
		{
			name:              "only Binance linear selected",
			binanceEnabled:    true,
			binanceMarkets:    []string{"linear"},
			bybitEnabled:      true,
			wantBinanceScopes: []upstream.Scope{upstream.BinanceLinear},
			wantBybit:         true,
		},
		{
			name:              "Bybit disabled",
			binanceEnabled:    true,
			binanceMarkets:    []string{"linear"},
			bybitEnabled:      false,
			wantBinanceScopes: []upstream.Scope{upstream.BinanceLinear},
			wantBybit:         false,
		},
		{
			name:              "Binance disabled",
			binanceEnabled:    false,
			binanceMarkets:    []string{"spot", "linear"},
			bybitEnabled:      true,
			wantBinanceScopes: nil,
			wantBybit:         true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Exchanges.Binance.Enabled = tt.binanceEnabled
			cfg.Exchanges.Binance.Markets = tt.binanceMarkets
			cfg.Exchanges.Bybit.Enabled = tt.bybitEnabled

			clients, err := newExchangeClients(cfg, noExchangeCalls{t}, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)

			require.NoError(t, err)
			assert.Len(t, clients.binance, len(tt.wantBinanceScopes))
			for _, scope := range tt.wantBinanceScopes {
				assert.NotNil(t, clients.binance[scope], "scope %s", scope)
			}
			assert.Equal(t, tt.wantBybit, clients.bybit != nil)
		})
	}
}

func TestExchangeAdmissionRejectsUnsupportedOperations(t *testing.T) {
	cases := []struct {
		name           string
		binanceMarkets []string
		scope          upstream.Scope
		operation      upstream.Operation
	}{
		{
			name:           "disabled Binance market",
			binanceMarkets: []string{"linear"},
			scope:          upstream.BinanceSpot,
			operation:      upstream.Tickers,
		},
		{
			name:           "independent Bybit statistics",
			binanceMarkets: []string{"spot", "linear"},
			scope:          upstream.Bybit,
			operation:      upstream.MarketStats,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Exchanges.Binance.Markets = tt.binanceMarkets
			clients, err := newExchangeClients(cfg, noExchangeCalls{t}, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)
			require.NoError(t, err)

			_, _, err = clients.admission.Begin(t.Context(), tt.scope, tt.operation)

			assert.ErrorIs(t, err, application.ErrUnsupportedOperation)
		})
	}
}

func TestExchangeInitializationRejectsAllExchangesDisabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.Exchanges.Binance.Enabled = false
	cfg.Exchanges.Bybit.Enabled = false

	clients, err := newExchangeClients(cfg, noExchangeCalls{t}, upstream.SystemClock{}, func(time.Duration) time.Duration { return 0 }, nil)

	assert.Error(t, err)
	assert.Nil(t, clients)
}
