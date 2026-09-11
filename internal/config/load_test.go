package config

import (
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadText(t *testing.T, text string, env ...string) Config {
	t.Helper()
	cfg, err := Load(strings.NewReader(text), env)
	require.NoError(t, err)

	return cfg
}

func TestDefaultsAndExample(t *testing.T) {
	cfg, err := Load(nil, nil)
	require.NoError(t, err)

	assert.Equal(t, Defaults(), cfg)

	file, err := os.Open("../../docs/examples/config-v1.yaml")
	require.NoError(t, err)

	defer func() { _ = file.Close() }()
	sample, err := Load(file, nil)
	require.NoError(t, err)

	assert.Equal(t, cfg, sample)

	cfg.Upstream.Limits.Bybit.Windows["http_requests_5s"] = Window{}
	assert.Equal(t, 600, Defaults().Upstream.Limits.Bybit.Windows["http_requests_5s"].Limit)
}

func TestPartialYAMLAndEnvironmentPrecedence(t *testing.T) {
	cfg := loadText(t, `server: {port: 8082}
klines: {max_history_candles: 500, request_timeout: 20s}
exchanges:
  binance:
    instruments: {refresh_interval: 12m}
    klines:
      max_candles_per_request: {linear: 1000}
`, "MDS_SERVER_PORT=8083", "MDS_KLINES_MAX_HISTORY_CANDLES=200", "MDS_KLINES_REQUEST_TIMEOUT=15s", "MDS_EXCHANGES_BYBIT_MARKETS=[\"linear\"]", "UNRELATED=ignored")
	assert.Equal(t, 8083, cfg.Server.Port)
	assert.Equal(t, 200, cfg.Klines.MaxHistoryCandles)
	assert.Equal(t, 15*time.Second, cfg.Klines.RequestTimeout)
	assert.Equal(t, 10*time.Second, cfg.HTTPClient.Timeout)

	assert.Equal(t, 12*time.Minute, cfg.Exchanges.Binance.Instruments.RefreshInterval)
	assert.Equal(t, 10*time.Minute, cfg.Exchanges.Bybit.Instruments.RefreshInterval)
	assert.Equal(t, 30*time.Second, cfg.Exchanges.Binance.MarketStats.RefreshInterval)

	assert.Equal(t, []string{"linear"}, cfg.Exchanges.Bybit.Markets)
	assert.Equal(t, 1000, cfg.Exchanges.Bybit.Klines.MaxCandlesPerRequest.Spot)
}

func TestHistoryAndPageLimits(t *testing.T) {
	for _, pair := range []struct {
		exchange, market string
		maximum          int
	}{
		{"bybit", "spot", 1000}, {"bybit", "linear", 1000}, {"binance", "spot", 1000}, {"binance", "linear", 1500},
	} {
		key := "MDS_EXCHANGES_" + strings.ToUpper(pair.exchange) + "_KLINES_MAX_CANDLES_PER_REQUEST_" + strings.ToUpper(pair.market)

		for _, value := range []int{1, 10, pair.maximum - 1, pair.maximum} {
			t.Run(pair.exchange+"_"+pair.market+fmt.Sprint(value), func(t *testing.T) {
				cfg := loadText(t, "", key+"="+fmt.Sprint(value), "MDS_UPSTREAM_LANES_PER_EXCHANGE_KLINES_MAX_ATTEMPTS=1000")
				limits := cfg.Exchanges.Bybit.Klines.MaxCandlesPerRequest
				if pair.exchange == "binance" {
					limits = cfg.Exchanges.Binance.Klines.MaxCandlesPerRequest
				}

				got := limits.Spot
				if pair.market == "linear" {
					got = limits.Linear
				}

				assert.Equal(t, value, got)
			})
		}

		for _, value := range []string{"0", "-1", "1.5", "true", "", fmt.Sprint(pair.maximum + 1), "9223372036854775808"} {
			t.Run(key+"_invalid_"+value, func(t *testing.T) {
				assertLoadError(t, nil, []string{key + "=" + value})
			})
		}
	}

	for _, value := range []string{"null", "\"\"", "0", "-1", "1.5", "true", "wrong", "9223372036854775808", "9223372036854775807"} {
		t.Run("history_"+value, func(t *testing.T) {
			assertLoadError(t, strings.NewReader("klines: {max_history_candles: "+value+"}"), nil)

			assertLoadError(t, nil, []string{"MDS_KLINES_MAX_HISTORY_CANDLES=" + value})
		})
	}

	assert.Equal(t, 1, loadText(t, "klines: {max_history_candles: 1}").Klines.MaxHistoryCandles)
}

func TestInvalidConfigurationSources(t *testing.T) {
	cases := []struct {
		name, yaml string
		env        []string
	}{
		{"malformed", "server: [", nil},
		{"null root", "null", nil},
		{"multiple documents", "{}\n---\n{}", nil},
		{"unknown key", "server: {typo: 1}", nil},
		{"unknown empty map", "missing: {}", nil},
		{"duplicate", "server: {port: 1, port: 2}", nil},
		{"duplicate section", "server: {}\nserver: {}", nil},
		{"numeric key", "server: {1: 2}", nil},
		{"null section", "server: null", nil},
		{"obsolete retention", "storage: {retention: {klines: {1m: 24h}}}", nil},
		{"bybit schedule", "exchanges: {bybit: {market_stats: {refresh_interval: 30s}}}", nil},
		{"unknown env", "", []string{"MDS_UNKNOWN=1"}},
		{"bad env shape", "", []string{"MDS_SERVER_PORT"}},
		{"duplicate env", "", []string{"MDS_SERVER_PORT=1", "MDS_SERVER_PORT=2"}},
		{"alias conflict", "", []string{"MDS_SENTRY_DSN=", "MDS_OBSERVABILITY_SENTRY_DSN="}},
		{"list CSV", "", []string{"MDS_MARKET_STATS_WINDOWS=24h"}},
		{"list null", "", []string{"MDS_MARKET_STATS_WINDOWS=null"}},
		{"list number", "", []string{"MDS_MARKET_STATS_WINDOWS=[24]"}},
		{"YAML numeric string", "server: {port: \"8080\"}", nil},
		{"YAML boolean string", "exchanges: {bybit: {enabled: \"false\"}}", nil},
		{"YAML sequence type", "market_stats: {windows: [24]}", nil},
		{"alias", "server: &server {}", nil},
		{"bad YAML remains error", "server: {port: true}", []string{"MDS_SERVER_PORT=8080"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertLoadError(t, strings.NewReader(tc.yaml), tc.env)
		})
	}
}

func TestAllDurationFields(t *testing.T) {
	walkLeaves(reflect.ValueOf(Defaults()), "", func(path string, v reflect.Value) {
		if v.Type() != reflect.TypeFor[time.Duration]() {
			return
		}

		name := "MDS_" + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))

		for _, raw := range []string{"", "0", "-1s", "garbage", "999999999999999999h"} {
			t.Run(name+"_"+raw, func(t *testing.T) {
				assertLoadError(t, nil, []string{name + "=" + raw})
			})
		}
	})
	cfg := loadText(t, "http_client: {timeout: 2s}\nklines: {request_timeout: 25s}")
	assert.Equal(t, 2*time.Second, cfg.HTTPClient.Timeout)
	assert.Equal(t, 25*time.Second, cfg.Klines.RequestTimeout)
	assert.Equal(t, 30*time.Second, cfg.WriteTimeout())
}

func TestSentryAndScalarTypes(t *testing.T) {
	cfg := loadText(t, "observability: {sentry: {environment: '${ENVIRONMENT}'}}", "MDS_SENTRY_DSN=https://public@example.com/1", "MDS_OBSERVABILITY_SENTRY_ENABLED=true")
	assert.Equal(t, "https://public@example.com/1", cfg.Observability.Sentry.DSN)
	assert.Equal(t, "${ENVIRONMENT}", cfg.Observability.Sentry.Environment)

	for _, raw := range []string{"NaN", "+Inf", "-0.1", "1.1", "bad", ""} {
		assertLoadError(t, nil, []string{"MDS_OBSERVABILITY_SENTRY_TRACES_SAMPLE_RATE=" + raw})
	}

	for _, rate := range []string{"0", "1"} {
		loadText(t, "observability: {sentry: {traces_sample_rate: "+rate+"}}")
	}

	for _, raw := range []string{"1", "yes", "", "garbage"} {
		assertLoadError(t, nil, []string{"MDS_EXCHANGES_BYBIT_ENABLED=" + raw})
	}

	cfg = Defaults()
	cfg.Observability.Sentry.TracesSampleRate = math.NaN()
	assert.Error(t, cfg.Validate())
}

func TestAllCountFieldsRejectNonpositiveValues(t *testing.T) {
	walkLeaves(reflect.ValueOf(Defaults()), "", func(path string, value reflect.Value) {
		if value.Kind() != reflect.Int || path == "upstream.safety_margin_percent" {
			return
		}

		name := "MDS_" + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))

		for _, raw := range []string{"0", "-1", "1.5", ""} {
			t.Run(name+"_"+raw, func(t *testing.T) {
				assertLoadError(t, nil, []string{name + "=" + raw})
			})
		}
	})
}

func TestLargeIntegerCeilingKeepsPrecision(t *testing.T) {
	cfg := loadText(t, "upstream: {safety_margin_percent: 0}", "MDS_UPSTREAM_LIMITS_BYBIT_WINDOWS_HTTP_REQUESTS_5S_LIMIT=9223372036854775807")
	assert.Equal(t, math.MaxInt64, cfg.Upstream.Limits.Bybit.Windows["http_requests_5s"].Limit)
}

func assertLoadError(t *testing.T, source io.Reader, environment []string) {
	t.Helper()
	_, err := Load(source, environment)
	assert.Error(t, err)
}
