package config

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"
)

func (c Config) Validate() error {
	checks := []func() error{
		c.validatePositiveValues,
		c.validateLocalSettings,
		c.validateSharesAndCooldowns,
		c.validateExchanges,
		c.validateCapacity,
		c.validateBudgets,
		c.validateObservability,
	}

	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}

	return nil
}

func (c Config) validatePositiveValues() error {
	var scalarError error
	walkLeaves(reflect.ValueOf(c), "", func(path string, value reflect.Value) {
		if scalarError != nil || path == "upstream.safety_margin_percent" {
			return
		}

		if (value.Kind() == reflect.Int || value.Kind() == reflect.Int64) && value.Int() <= 0 {
			scalarError = fmt.Errorf("%s must be positive", path)
		}
	})
	if scalarError != nil {
		return scalarError
	}

	return nil
}

func (c Config) validateLocalSettings() error {
	if c.Storage.Driver != "memory" {
		return fmt.Errorf("storage.driver must be memory")
	}

	for _, listener := range []struct {
		name, host string
		port       int
	}{
		{"grpc", c.Server.GRPC.Host, c.Server.GRPC.Port}, {"http", c.Server.HTTP.Host, c.Server.HTTP.Port},
	} {
		if listener.host != "" && net.ParseIP(listener.host) == nil && !validHostname(listener.host) {
			return fmt.Errorf("server.%s.host must be an IP address or hostname", listener.name)
		}
		if listener.port > 65535 {
			return fmt.Errorf("server.%s.port must be in 1..65535", listener.name)
		}
	}
	if c.Server.GRPC.Port == c.Server.HTTP.Port && overlappingHosts(c.Server.GRPC.Host, c.Server.HTTP.Host) {
		return fmt.Errorf("server.grpc and server.http addresses overlap")
	}
	if c.Server.HTTP.MaxHeaderBytes > math.MaxInt-4096 || c.HTTPClient.MaxResponseBytes == math.MaxInt {
		return fmt.Errorf("HTTP byte limits overflow internal bounds")
	}
	if c.Server.GRPC.MaxRequestBytes > math.MaxInt32 || c.Server.GRPC.MaxResponseBytes > math.MaxInt32 || c.Server.GRPC.MaxHeaderBytes > math.MaxInt32 {
		return fmt.Errorf("gRPC byte limits must not exceed 2147483647")
	}

	if c.Klines.MaxHistoryCandles > math.MaxInt64/(31*24*60*60) {
		return fmt.Errorf("klines.max_history_candles overflows calendar arithmetic")
	}

	lifetime := max(c.Klines.RequestTimeout, c.Klines.FillTimeout, c.Server.SnapshotTimeout)
	if lifetime > time.Duration(math.MaxInt64)-WriteGrace || c.Server.ShutdownTimeout < lifetime+WriteGrace {
		return fmt.Errorf("server.shutdown_timeout must cover caller and fill lifetimes plus 5s")
	}

	if !slices.Equal(c.MarketStats.Windows, []string{"24h"}) {
		return fmt.Errorf("market_stats.windows must be [24h]")
	}

	if c.HTTPClient.Retry.InitialBackoff > c.HTTPClient.Retry.MaxBackoff {
		return fmt.Errorf("retry initial_backoff exceeds max_backoff")
	}

	return nil
}

func (c Config) validateSharesAndCooldowns() error {
	if c.Upstream.Binance.StopThresholdPercent < 1 || c.Upstream.Binance.StopThresholdPercent > 99 {
		return fmt.Errorf("upstream.binance.stop_threshold_percent must be in 1..99")
	}

	margin := c.Upstream.SafetyMarginPercent
	if margin < 0 || margin > 99 {
		return fmt.Errorf("upstream.safety_margin_percent must be in 0..99")
	}

	shares := c.Upstream.OperationSharePercent
	shareValues := []int{shares.Tickers, shares.Klines, shares.Instruments, shares.MarketStats}
	remaining := 100

	for _, share := range shareValues {
		if share < 1 || share > remaining {
			return fmt.Errorf("operation shares must be positive integers totaling 100")
		}

		remaining -= share
	}

	if remaining != 0 {
		return fmt.Errorf("operation shares must total 100")
	}

	cooldown := c.Upstream.Cooldown
	if cooldown.Binance429 < time.Minute ||
		cooldown.Binance418 < 72*time.Hour ||
		cooldown.Bybit429 < time.Minute ||
		cooldown.Bybit10006 < time.Minute ||
		cooldown.Bybit403AccessTooFrequent < 10*time.Minute {
		return fmt.Errorf("upstream cooldown is below its required minimum")
	}

	return nil
}

func (c Config) validateExchanges() error {
	bybit, binance := c.Exchanges.Bybit, c.Exchanges.Binance
	if !bybit.Enabled && !binance.Enabled {
		return fmt.Errorf("at least one exchange must be enabled")
	}

	for _, exchange := range []struct {
		name          string
		enabled       bool
		markets       []string
		pages, maxima Markets[int]
	}{
		{"bybit", bybit.Enabled, bybit.Markets, bybit.Klines.MaxCandlesPerRequest, Markets[int]{BybitKlineMaximum, BybitKlineMaximum}},
		{"binance", binance.Enabled, binance.Markets, binance.Klines.MaxCandlesPerRequest, Markets[int]{BinanceSpotKlineMaximum, BinanceLinearKlineMaximum}},
	} {
		if exchange.enabled && len(exchange.markets) == 0 {
			return fmt.Errorf("%s must enable at least one market", exchange.name)
		}

		seen := make(map[string]bool)

		for _, market := range exchange.markets {
			if (market != "spot" && market != "linear") || seen[market] {
				return fmt.Errorf("%s has an invalid or duplicate market", exchange.name)
			}

			seen[market] = true
		}

		if exchange.pages.Spot > exchange.maxima.Spot || exchange.pages.Linear > exchange.maxima.Linear {
			return fmt.Errorf("%s kline page limit exceeds the endpoint maximum", exchange.name)
		}

		for _, market := range exchange.markets {
			if !exchange.enabled {
				continue
			}

			pageSize := exchange.pages.Spot
			if market == "linear" {
				pageSize = exchange.pages.Linear
			}

			pages := (c.Klines.MaxHistoryCandles-1)/pageSize + 1
			if pages > c.Upstream.LanesPerExchange.Klines.MaxAttempts {
				return fmt.Errorf("%s %s history needs more pages than the kline attempt bound", exchange.name, market)
			}
		}
	}

	return nil
}

func (c Config) validateObservability() error {
	sentry := c.Observability.Sentry
	if math.IsNaN(sentry.TracesSampleRate) || math.IsInf(sentry.TracesSampleRate, 0) || sentry.TracesSampleRate < 0 || sentry.TracesSampleRate > 1 {
		return fmt.Errorf("observability.sentry.traces_sample_rate must be in 0..1")
	}

	if strings.TrimSpace(sentry.Environment) == "" {
		return fmt.Errorf("observability.sentry.environment must not be empty")
	}

	if sentry.Enabled || sentry.DSN != "" {
		u, err := url.Parse(sentry.DSN)
		if err != nil || u == nil ||
			(u.Scheme != "https" && u.Scheme != "http") ||
			u.Hostname() == "" || u.User == nil || u.User.Username() == "" ||
			strings.Trim(u.Path, "/") == "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("observability.sentry.dsn must be a valid DSN")
		}
	}

	metrics := c.Observability.Prometheus.Path
	if !strings.HasPrefix(metrics, "/") || metrics == "/" || path.Clean(metrics) != metrics ||
		strings.ContainsAny(metrics, "{}?%# \t\r\n") || strings.HasPrefix(metrics, "//") ||
		metrics == "/health" || metrics == "/ready" ||
		strings.HasPrefix(metrics, "/api") || strings.HasPrefix(metrics, "/debug") {
		return fmt.Errorf("observability.prometheus.path must be an absolute, non-reserved HTTP path")
	}

	return nil
}

func validHostname(host string) bool {
	if len(host) > 253 {
		return false
	}

	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}

		for _, r := range label {
			isLetter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
			isDigit := r >= '0' && r <= '9'

			if !isLetter && !isDigit && r != '-' {
				return false
			}
		}
	}

	return true
}

func (c Config) validateCapacity() error {
	lanes := c.Upstream.LanesPerExchange
	slots, waiters := c.Upstream.MaxHTTPInflight, c.Upstream.MaxAdmissionWaiters

	for _, enabled := range []bool{c.Exchanges.Bybit.Enabled, c.Exchanges.Binance.Enabled} {
		if !enabled {
			continue
		}

		for _, lane := range []Lane{
			{HTTPSlots: lanes.Tickers.HTTPSlots, Waiters: lanes.Tickers.Waiters},
			{HTTPSlots: lanes.Instruments.HTTPSlots, Waiters: lanes.Instruments.Waiters},
			lanes.Klines,
		} {
			if lane.HTTPSlots > slots || lane.Waiters > waiters {
				return fmt.Errorf("global upstream caps do not cover enabled lanes")
			}

			slots -= lane.HTTPSlots
			waiters -= lane.Waiters
		}
	}

	if c.Exchanges.Binance.Enabled && (lanes.MarketStats.HTTPSlots > slots || lanes.MarketStats.Waiters > waiters) {
		return fmt.Errorf("global upstream caps do not cover the statistics lane")
	}

	fills := c.Klines
	if fills.MaxActiveFillsPerExchange > fills.MaxActiveFills || fills.MaxActiveFillsPerExchange > lanes.Klines.HTTPSlots {
		return fmt.Errorf("kline fill caps exceed global, per-exchange, or lane capacity")
	}

	return nil
}

// Percentage uses quotient and remainder so large finite ceilings cannot overflow.
func percentage(value, percent int) int { return value/100*percent + value%100*percent/100 }

// allowances applies the common margin first, then independent reserved shares.
// Shared ticker/statistics providers combine percentages before rounding.
func (c Config) allowances(window Window, sharedStatistics bool) (int, Operations[int]) {
	threshold := c.Upstream.Binance.StopThresholdPercent
	if sharedStatistics {
		threshold = 100 - c.Upstream.SafetyMarginPercent
	}
	common := percentage(window.Limit, threshold)
	if !window.SplitOperations {
		return common, Operations[int]{Instruments: common}
	}

	s := c.Upstream.OperationSharePercent
	if sharedStatistics {
		s.Tickers += s.MarketStats
		s.MarketStats = 0
	}

	return common, Operations[int]{percentage(common, s.Tickers), percentage(common, s.Klines), percentage(common, s.Instruments), percentage(common, s.MarketStats)}
}

func (c Config) validateBudgets() error {
	defaults := Defaults().Upstream.Limits

	for _, scope := range []struct {
		name             string
		actual, defaults Scope
		enabled, shared  bool
		costs            Operations[int]
	}{
		{"binance_spot", c.Upstream.Limits.BinanceSpot, defaults.BinanceSpot, c.Exchanges.Binance.Enabled && slices.Contains(c.Exchanges.Binance.Markets, "spot"), false, Operations[int]{8, 2, 20, 80}},
		{
			name:     "binance_linear",
			actual:   c.Upstream.Limits.BinanceLinear,
			defaults: defaults.BinanceLinear,
			enabled:  c.Exchanges.Binance.Enabled && slices.Contains(c.Exchanges.Binance.Markets, "linear"),
			costs: Operations[int]{
				Tickers:     17,
				Klines:      linearKlineCost(c.Exchanges.Binance.Klines.MaxCandlesPerRequest.Linear),
				Instruments: 1,
				MarketStats: 40,
			},
		},
		{"bybit", c.Upstream.Limits.Bybit, defaults.Bybit, c.Exchanges.Bybit.Enabled, true, Operations[int]{1, 1, 1, 0}},
	} {
		if len(scope.actual.Windows) != len(scope.defaults.Windows) {
			return fmt.Errorf("%s has invalid allocation windows", scope.name)
		}

		for id, w := range scope.actual.Windows {
			d, exists := scope.defaults.Windows[id]
			if !exists || w.Unit != d.Unit || w.Window != d.Window || w.SplitOperations != d.SplitOperations {
				return fmt.Errorf("%s.%s changes a fixed allocation window", scope.name, id)
			}

			if !scope.enabled {
				continue
			}

			if !scope.shared {
				w.Limit = min(w.Limit, d.Limit)
			}
			common, allocation := c.allowances(w, scope.shared)
			cost := scope.costs
			if w.Unit == "requests" {
				cost = Operations[int]{1, 1, 1, 1}
				if scope.shared {
					cost.MarketStats = 0
				}

				if id == "raw_requests_5m" {
					cost.Tickers = 2
				}
			}

			if !w.SplitOperations {
				if common < 1 {
					return fmt.Errorf("%s.%s cannot admit funding metadata", scope.name, id)
				}

				continue
			}

			if allocation.Tickers < cost.Tickers || allocation.Klines < cost.Klines || allocation.Instruments < cost.Instruments || allocation.MarketStats < cost.MarketStats {
				return fmt.Errorf("%s.%s cannot admit a permitted operation", scope.name, id)
			}

			if !scope.shared {
				interval := c.Exchanges.Binance.MarketStats.RefreshInterval
				cycles := (w.Window-1)/interval + 1
				if int64(cycles) > int64(allocation.MarketStats/cost.MarketStats) {
					return fmt.Errorf("%s.%s cannot support the statistics refresh interval", scope.name, id)
				}
			}
		}
	}

	return nil
}

func linearKlineCost(limit int) int {
	switch {
	case limit < 100:
		return 1
	case limit < 500:
		return 2
	case limit <= 1000:
		return 5
	default:
		return 10
	}
}

// DNS names and platform-specific wildcard behavior are checked by binding both listeners.
func overlappingHosts(first, second string) bool {
	if strings.EqualFold(first, second) {
		return true
	}
	a, b := net.ParseIP(first), net.ParseIP(second)
	if first == "" || second == "" {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Equal(b) {
		return true
	}
	return (a.IsUnspecified() || b.IsUnspecified()) && ((a.To4() == nil) == (b.To4() == nil))
}
