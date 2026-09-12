package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCanonicalMarketValues(t *testing.T) {
	for _, value := range []Exchange{ExchangeBinance, ExchangeBybit} {
		assert.True(t, value.Valid())
	}
	for _, value := range []Market{MarketSpot, MarketLinear} {
		assert.True(t, value.Valid())
	}
	for _, value := range []InstrumentStatus{
		InstrumentStatusUnknown, InstrumentStatusPreLaunch, InstrumentStatusTrading,
		InstrumentStatusHalted, InstrumentStatusCancelOnly, InstrumentStatusSettling, InstrumentStatusClosed,
	} {
		assert.True(t, value.Valid())
	}
	for _, value := range []Exchange{"", "Binance", " binance", "bybit ", "other"} {
		assert.False(t, value.Valid())
	}
	for _, value := range []Market{"", "SPOT", " spot", "linear ", "inverse", "futures"} {
		assert.False(t, value.Valid())
	}
	for _, value := range []InstrumentStatus{"", "Trading", "trading ", " pre_launch", "prelaunch", "open", "other"} {
		assert.False(t, value.Valid())
	}
}
