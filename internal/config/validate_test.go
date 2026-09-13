package config

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvalidSettings(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
	}{
		{"driver", func(c *Config) { c.Storage.Driver = "redis" }},
		{"port", func(c *Config) { c.Server.Port = 65536 }},
		{"host URL", func(c *Config) { c.Server.Host = "https://localhost" }},
		{"host label", func(c *Config) { c.Server.Host = "-invalid" }},
		{"header overflow", func(c *Config) { c.Server.MaxHeaderBytes = math.MaxInt }},
		{"body overflow", func(c *Config) { c.HTTPClient.MaxResponseBytes = math.MaxInt }},
		{"shutdown", func(c *Config) { c.Server.ShutdownTimeout = 34 * time.Second }},
		{"write overflow", func(c *Config) { c.Klines.RequestTimeout = time.Duration(math.MaxInt64) }},
		{"window", func(c *Config) { c.MarketStats.Windows = []string{"1h"} }},
		{"window extra", func(c *Config) { c.MarketStats.Windows = []string{"24h", "24h"} }},
		{"window empty", func(c *Config) { c.MarketStats.Windows = nil }},
		{"retry order", func(c *Config) { c.HTTPClient.Retry.InitialBackoff = 3 * time.Second }},
		{"margin negative", func(c *Config) { c.Upstream.SafetyMarginPercent = -1 }},
		{"margin high", func(c *Config) { c.Upstream.SafetyMarginPercent = 100 }},
		{"shares sum", func(c *Config) { c.Upstream.OperationSharePercent.Tickers = 59 }},
		{"shares overflow", func(c *Config) { c.Upstream.OperationSharePercent.Tickers = math.MaxInt }},
		{"no exchange", func(c *Config) {
			c.Exchanges.Bybit.Enabled = false
			c.Exchanges.Binance.Enabled = false
		}},
		{"no market", func(c *Config) { c.Exchanges.Bybit.Markets = nil }},
		{"unsupported market", func(c *Config) { c.Exchanges.Bybit.Markets = []string{"inverse"} }},
		{"duplicate market", func(c *Config) { c.Exchanges.Binance.Markets = []string{"spot", "spot"} }},
		{"disabled invalid market", func(c *Config) {
			c.Exchanges.Bybit.Enabled = false
			c.Exchanges.Bybit.Markets = []string{"inverse"}
		}},
		{"page attempt bound", func(c *Config) { c.Exchanges.Binance.Klines.MaxCandlesPerRequest.Spot = 1 }},
		{"HTTP capacity", func(c *Config) { c.Upstream.MaxHTTPInflight = 19 }},
		{"stats HTTP capacity", func(c *Config) { c.Upstream.MaxHTTPInflight = 21 }},
		{"waiter capacity", func(c *Config) { c.Upstream.MaxAdmissionWaiters = 47 }},
		{"stats waiter capacity", func(c *Config) { c.Upstream.MaxAdmissionWaiters = 51 }},
		{"global fill capacity", func(c *Config) { c.Klines.MaxActiveFills = 5 }},
		{"fill lane capacity", func(c *Config) { c.Klines.MaxActiveFillsPerExchange = 7 }},
		{"Binance 429", func(c *Config) { c.Upstream.Cooldown.Binance429 = 59 * time.Second }},
		{"Binance 418", func(c *Config) { c.Upstream.Cooldown.Binance418 = 71 * time.Hour }},
		{"Bybit 429", func(c *Config) { c.Upstream.Cooldown.Bybit429 = 59 * time.Second }},
		{"Bybit 10006", func(c *Config) { c.Upstream.Cooldown.Bybit10006 = 59 * time.Second }},
		{"Bybit 403", func(c *Config) { c.Upstream.Cooldown.Bybit403AccessTooFrequent = 9 * time.Minute }},
		{"Sentry missing DSN", func(c *Config) { c.Observability.Sentry.Enabled = true }},
		{"Sentry invalid DSN", func(c *Config) { c.Observability.Sentry.DSN = "secret-value" }},
		{"Sentry environment", func(c *Config) { c.Observability.Sentry.Environment = " " }},
		{"Sentry infinity", func(c *Config) { c.Observability.Sentry.TracesSampleRate = math.Inf(1) }},
		{"metrics relative path", func(c *Config) { c.Observability.Prometheus.Path = "metrics" }},
		{"metrics route conflict", func(c *Config) { c.Observability.Prometheus.Path = "/health" }},
		{"metrics pattern", func(c *Config) { c.Observability.Prometheus.Path = "/{route}" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			tc.change(&cfg)
			assert.Error(t, cfg.Validate())
		})
	}
}

func TestOperationShares(t *testing.T) {
	cfg := loadText(t, "upstream: {operation_share_percent: {tickers: 50, klines: 40}}")
	assert.Equal(t, (Operations[int]{50, 40, 5, 5}), cfg.Upstream.OperationSharePercent)

	cfg = loadText(t, "upstream: {operation_share_percent: {tickers: 50, klines: 40}}",
		"MDS_UPSTREAM_OPERATION_SHARE_PERCENT_TICKERS=55", "MDS_UPSTREAM_OPERATION_SHARE_PERCENT_KLINES=25",
		"MDS_UPSTREAM_OPERATION_SHARE_PERCENT_INSTRUMENTS=10", "MDS_UPSTREAM_OPERATION_SHARE_PERCENT_MARKET_STATS=10")
	assert.Equal(t, (Operations[int]{55, 25, 10, 10}), cfg.Upstream.OperationSharePercent)

	assert.Equal(t, Defaults().Upstream.OperationSharePercent, loadText(t, "upstream: {operation_share_percent: {}}").Upstream.OperationSharePercent)

	for _, key := range []string{"tickers", "klines", "instruments", "market_stats"} {
		for _, value := range []string{"null", "\"\"", "0", "-1", "101", "1.5", "true", "invalid", "9223372036854775808"} {
			t.Run(key+"_"+value, func(t *testing.T) {
				assertLoadError(t, strings.NewReader("upstream: {operation_share_percent: {"+key+": "+value+"}}"), nil)

				assertLoadError(t, nil, []string{"MDS_UPSTREAM_OPERATION_SHARE_PERCENT_" + strings.ToUpper(key) + "=" + value})
			})
		}
	}
}

func TestAllowances(t *testing.T) {
	cfg := Defaults()
	cases := []struct {
		name   string
		window Window
		shared bool
		common int
		shares Operations[int]
	}{
		{"spot", cfg.Upstream.Limits.BinanceSpot.Windows["request_weight_1m"], false, 5400, Operations[int]{3240, 1620, 270, 270}},
		{"linear", cfg.Upstream.Limits.BinanceLinear.Windows["request_weight_1m"], false, 2160, Operations[int]{1296, 648, 108, 108}},
		{"raw", cfg.Upstream.Limits.BinanceSpot.Windows["raw_requests_5m"], false, 270000, Operations[int]{162000, 81000, 13500, 13500}},
		{"funding", cfg.Upstream.Limits.BinanceLinear.Windows["funding_requests_5m"], false, 450, Operations[int]{Instruments: 450}},
		{"Bybit shared", cfg.Upstream.Limits.Bybit.Windows["http_requests_5s"], true, 480, Operations[int]{312, 144, 24, 0}},
		{"combine before floor", Window{Limit: 24, SplitOperations: true}, true, 19, Operations[int]{12, 5, 0, 0}},
		{"separate floors", Window{Limit: 24, SplitOperations: true}, false, 21, Operations[int]{12, 6, 1, 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			common, shares := cfg.allowances(tc.window, tc.shared)
			assert.Equal(t, tc.common, common)
			assert.Equal(t, tc.shares, shares)
		})
	}

	cfg.Upstream.SafetyMarginPercent = 0
	common, _ := cfg.allowances(Window{Limit: math.MaxInt, SplitOperations: true}, true)
	assert.Equal(t, math.MaxInt, common)
}

func TestEveryBudgetWindowThreshold(t *testing.T) {
	for _, tc := range []struct {
		scope, window string
		threshold     int
	}{
		{"binance_spot", "request_weight_1m", 3556},
		{"binance_spot", "raw_requests_5m", 223},
		{"binance_linear", "request_weight_1m", 1778},
		{"binance_linear", "funding_requests_5m", 2},
		{"bybit", "http_requests_5s", 25},
	} {
		key := "MDS_UPSTREAM_LIMITS_" + strings.ToUpper(tc.scope) + "_WINDOWS_" + strings.ToUpper(tc.window) + "_LIMIT="
		t.Run(tc.scope+"_"+tc.window, func(t *testing.T) {
			loadText(t, "", key+fmt.Sprint(tc.threshold))
			assertLoadError(t, nil, []string{key + fmt.Sprint(tc.threshold-1)})
		})
	}
}

func TestFixedWindowsAndSchedules(t *testing.T) {
	for _, field := range []string{"unit: requests", "window: 2m", "split_operations: false"} {
		assertLoadError(t, strings.NewReader("upstream: {limits: {binance_spot: {windows: {request_weight_1m: {"+field+"}}}}}"), nil)
	}

	assertLoadError(t, nil, []string{"MDS_EXCHANGES_BINANCE_MARKET_STATS_REFRESH_INTERVAL=10s"})

	loadText(t, "", "MDS_EXCHANGES_BINANCE_MARKET_STATS_REFRESH_INTERVAL=1m", "MDS_UPSTREAM_LIMITS_BINANCE_SPOT_WINDOWS_REQUEST_WEIGHT_1M_LIMIT=2000", "MDS_UPSTREAM_LIMITS_BINANCE_LINEAR_WINDOWS_REQUEST_WEIGHT_1M_LIMIT=1000")
}

func TestPermittedLinearPageCosts(t *testing.T) {
	for _, tc := range []struct{ limit, cost int }{
		{1, 1}, {99, 1}, {100, 2}, {499, 2}, {500, 5}, {1000, 5}, {1001, 10}, {1500, 10},
	} {
		t.Run(fmt.Sprint(tc.limit), func(t *testing.T) {
			cfg := Defaults()
			cfg.Klines.MaxHistoryCandles = 1
			cfg.Exchanges.Binance.Markets = []string{"linear"}
			cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest.Linear = tc.limit
			cfg.Upstream.SafetyMarginPercent = 0
			cfg.Upstream.OperationSharePercent = Operations[int]{30, 1, 19, 50}
			window := cfg.Upstream.Limits.BinanceLinear.Windows["request_weight_1m"]
			window.Limit = (tc.cost*100*100 + 89) / 90
			cfg.Exchanges.Binance.MarketStats.RefreshInterval = time.Minute
			cfg.Upstream.Limits.BinanceLinear.Windows["request_weight_1m"] = window
			require.NoError(t, cfg.Validate())

			window.Limit--
			cfg.Upstream.Limits.BinanceLinear.Windows["request_weight_1m"] = window
			assert.Error(t, cfg.Validate())
		})
	}
}

func TestDisabledProviderAndMinimumLaneCapacity(t *testing.T) {
	for _, name := range []string{"bybit", "binance"} {
		t.Run(name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Exchanges.Bybit.Enabled = name == "bybit"
			cfg.Exchanges.Binance.Enabled = name == "binance"
			cfg.Upstream.MaxHTTPInflight = 10
			cfg.Upstream.MaxAdmissionWaiters = 24
			if name == "binance" {
				cfg.Upstream.MaxHTTPInflight = 12
				cfg.Upstream.MaxAdmissionWaiters = 28
			}

			require.NoError(t, cfg.Validate())
		})
	}
}
