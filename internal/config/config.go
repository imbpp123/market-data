// Package config loads and validates process settings before any external work.
package config

import "time"

const (
	DefaultHistoryCandles     = 1000
	DefaultKlineTimeout       = 30 * time.Second
	BybitKlineMaximum         = 1000
	BinanceSpotKlineMaximum   = 1000
	BinanceLinearKlineMaximum = 1500
	WriteGrace                = 5 * time.Second
)

type Config struct {
	Server        Server        `yaml:"server"`
	Storage       Storage       `yaml:"storage"`
	Klines        Klines        `yaml:"klines"`
	MarketStats   MarketStats   `yaml:"market_stats"`
	Upstream      Upstream      `yaml:"upstream"`
	Exchanges     Exchanges     `yaml:"exchanges"`
	HTTPClient    HTTPClient    `yaml:"http_client"`
	Observability Observability `yaml:"observability"`
}

type Server struct {
	GRPC                GRPCServer    `yaml:"grpc"`
	HTTP                HTTPServer    `yaml:"http"`
	ShutdownTimeout     time.Duration `yaml:"shutdown_timeout"`
	MaxSnapshotRequests int           `yaml:"max_snapshot_requests"`
	SnapshotTimeout     time.Duration `yaml:"snapshot_timeout"`
}

type GRPCServer struct {
	Host             string `yaml:"host"`
	Port             int    `yaml:"port"`
	MaxRequestBytes  int    `yaml:"max_request_bytes"`
	MaxResponseBytes int    `yaml:"max_response_bytes"`
	MaxHeaderBytes   int    `yaml:"max_header_bytes"`
}

type HTTPServer struct {
	Host              string        `yaml:"host"`
	Port              int           `yaml:"port"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	MaxHeaderBytes    int           `yaml:"max_header_bytes"`
	MaxQueryBytes     int           `yaml:"max_query_bytes"`
}

type Storage struct {
	Driver          string        `yaml:"driver"`
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
}

type Klines struct {
	RequestTimeout            time.Duration `yaml:"request_timeout"`
	MaxHistoryCandles         int           `yaml:"max_history_candles"`
	MaxCallers                int           `yaml:"max_callers"`
	MaxActiveFills            int           `yaml:"max_active_fills"`
	MaxActiveFillsPerExchange int           `yaml:"max_active_fills_per_exchange"`
	FillTimeout               time.Duration `yaml:"fill_timeout"`
}

type MarketStats struct {
	Windows []string `yaml:"windows"`
}

type Operations[T any] struct {
	Tickers     T `yaml:"tickers"`
	Klines      T `yaml:"klines"`
	Instruments T `yaml:"instruments"`
	MarketStats T `yaml:"market_stats"`
}

type BinanceUpstream struct {
	StopThresholdPercent   int           `yaml:"stop_threshold_percent"`
	CatalogRefreshInterval time.Duration `yaml:"catalog_refresh_interval"`
}

type Upstream struct {
	Binance               BinanceUpstream `yaml:"binance"`
	SafetyMarginPercent   int             `yaml:"safety_margin_percent"`
	OperationSharePercent Operations[int] `yaml:"operation_share_percent"`
	MaxHTTPInflight       int             `yaml:"max_http_inflight"`
	MaxAdmissionWaiters   int             `yaml:"max_admission_waiters"`
	AdmissionTimeout      time.Duration   `yaml:"admission_timeout"`
	LanesPerExchange      Lanes           `yaml:"lanes_per_exchange"`
	Limits                Limits          `yaml:"limits"`
	Cooldown              Cooldown        `yaml:"cooldown"`
}

type Lane struct {
	HTTPSlots   int `yaml:"http_slots"`
	Waiters     int `yaml:"waiters"`
	MaxAttempts int `yaml:"max_attempts"`
}

type CycleLane struct {
	HTTPSlots    int           `yaml:"http_slots"`
	Waiters      int           `yaml:"waiters"`
	CycleTimeout time.Duration `yaml:"cycle_timeout"`
	MaxAttempts  int           `yaml:"max_attempts"`
}

type Lanes struct {
	Tickers     CycleLane `yaml:"tickers"`
	Instruments CycleLane `yaml:"instruments"`
	Klines      Lane      `yaml:"klines"`
	MarketStats CycleLane `yaml:"market_stats"`
}

type Limits struct {
	BinanceSpot   Scope `yaml:"binance_spot"`
	BinanceLinear Scope `yaml:"binance_linear"`
	Bybit         Scope `yaml:"bybit"`
}

type Scope struct {
	MinRequestSpacing time.Duration     `yaml:"min_request_spacing"`
	Windows           map[string]Window `yaml:"windows"`
}

type Window struct {
	ExplicitLimit   bool          `yaml:"-"`
	Unit            string        `yaml:"unit"`
	Window          time.Duration `yaml:"window"`
	Limit           int           `yaml:"limit"`
	SplitOperations bool          `yaml:"split_operations"`
}

type Cooldown struct {
	Binance429                time.Duration `yaml:"binance_429"`
	Binance418                time.Duration `yaml:"binance_418"`
	Bybit429                  time.Duration `yaml:"bybit_429"`
	Bybit10006                time.Duration `yaml:"bybit_10006"`
	Bybit403AccessTooFrequent time.Duration `yaml:"bybit_403_access_too_frequent"`
}

type Exchanges struct {
	Bybit   Bybit   `yaml:"bybit"`
	Binance Binance `yaml:"binance"`
}

type Refresh struct {
	RefreshInterval time.Duration `yaml:"refresh_interval"`
}
type PageLimits struct {
	MaxCandlesPerRequest Markets[int] `yaml:"max_candles_per_request"`
}
type Markets[T any] struct {
	Spot   T `yaml:"spot"`
	Linear T `yaml:"linear"`
}

type Bybit struct {
	Enabled     bool       `yaml:"enabled"`
	Markets     []string   `yaml:"markets"`
	Instruments Refresh    `yaml:"instruments"`
	Klines      PageLimits `yaml:"klines"`
}

type Binance struct {
	Enabled     bool       `yaml:"enabled"`
	Markets     []string   `yaml:"markets"`
	Instruments Refresh    `yaml:"instruments"`
	Klines      PageLimits `yaml:"klines"`
	MarketStats Refresh    `yaml:"market_stats"`
}

type HTTPClient struct {
	Timeout          time.Duration `yaml:"timeout"`
	MaxResponseBytes int           `yaml:"max_response_bytes"`
	Retry            Retry         `yaml:"retry"`
}

type Retry struct {
	MaxAttempts    int           `yaml:"max_attempts"`
	InitialBackoff time.Duration `yaml:"initial_backoff"`
	MaxBackoff     time.Duration `yaml:"max_backoff"`
}

type Observability struct {
	Sentry     Sentry     `yaml:"sentry"`
	Prometheus Prometheus `yaml:"prometheus"`
	Stats      Stats      `yaml:"stats"`
}

type Sentry struct {
	Enabled          bool    `yaml:"enabled"`
	DSN              string  `yaml:"dsn"`
	Environment      string  `yaml:"environment"`
	TracesSampleRate float64 `yaml:"traces_sample_rate"`
}

type Prometheus struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

type Stats struct {
	Enabled         bool `yaml:"enabled"`
	EndpointEnabled bool `yaml:"endpoint_enabled"`
}

func Defaults() Config {
	return Config{
		Server: Server{
			GRPC:                GRPCServer{Host: "0.0.0.0", Port: 9090, MaxRequestBytes: 8192, MaxResponseBytes: 16777216, MaxHeaderBytes: 32768},
			HTTP:                HTTPServer{Host: "0.0.0.0", Port: 8080, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 32768, MaxQueryBytes: 8192},
			ShutdownTimeout:     35 * time.Second,
			MaxSnapshotRequests: 64,
			SnapshotTimeout:     5 * time.Second,
		},
		Storage: Storage{Driver: "memory", CleanupInterval: time.Hour},
		Klines: Klines{
			RequestTimeout:            DefaultKlineTimeout,
			MaxHistoryCandles:         DefaultHistoryCandles,
			MaxCallers:                64,
			MaxActiveFills:            12,
			MaxActiveFillsPerExchange: 6,
			FillTimeout:               DefaultKlineTimeout,
		},
		MarketStats: MarketStats{Windows: []string{"24h"}},
		Upstream: Upstream{
			Binance:               BinanceUpstream{StopThresholdPercent: 90, CatalogRefreshInterval: time.Hour},
			SafetyMarginPercent:   20,
			OperationSharePercent: Operations[int]{Tickers: 60, Klines: 30, Instruments: 5, MarketStats: 5},
			MaxHTTPInflight:       24,
			MaxAdmissionWaiters:   56,
			AdmissionTimeout:      10 * time.Second,
			LanesPerExchange: Lanes{
				Tickers:     CycleLane{HTTPSlots: 2, Waiters: 4, CycleTimeout: 15 * time.Second, MaxAttempts: 9},
				Instruments: CycleLane{HTTPSlots: 2, Waiters: 4, CycleTimeout: time.Minute, MaxAttempts: 100},
				Klines:      Lane{HTTPSlots: 6, Waiters: 16, MaxAttempts: 12},
				MarketStats: CycleLane{HTTPSlots: 2, Waiters: 4, CycleTimeout: 15 * time.Second, MaxAttempts: 3},
			},
			Limits: Limits{
				BinanceSpot: Scope{
					MinRequestSpacing: 20 * time.Millisecond,
					Windows: map[string]Window{
						"request_weight_1m": {Unit: "weight", Window: time.Minute, Limit: 6000, SplitOperations: true},
						"raw_requests_5m":   {Unit: "requests", Window: 5 * time.Minute, Limit: 300000, SplitOperations: true},
					},
				},
				BinanceLinear: Scope{
					MinRequestSpacing: 20 * time.Millisecond,
					Windows: map[string]Window{
						"request_weight_1m":   {Unit: "weight", Window: time.Minute, Limit: 2400, SplitOperations: true},
						"funding_requests_5m": {Unit: "requests", Window: 5 * time.Minute, Limit: 500, SplitOperations: false},
					},
				},
				Bybit: Scope{
					MinRequestSpacing: 10 * time.Millisecond,
					Windows: map[string]Window{
						"http_requests_5s": {Unit: "requests", Window: 5 * time.Second, Limit: 600, SplitOperations: true},
					},
				},
			},
			Cooldown: Cooldown{
				Binance429:                time.Minute,
				Binance418:                72 * time.Hour,
				Bybit429:                  time.Minute,
				Bybit10006:                time.Minute,
				Bybit403AccessTooFrequent: 10 * time.Minute,
			},
		},
		Exchanges: Exchanges{
			Bybit: Bybit{
				Enabled:     true,
				Markets:     []string{"spot", "linear"},
				Instruments: Refresh{RefreshInterval: 10 * time.Minute},
				Klines:      PageLimits{MaxCandlesPerRequest: Markets[int]{Spot: BybitKlineMaximum, Linear: BybitKlineMaximum}},
			},
			Binance: Binance{
				Enabled:     true,
				Markets:     []string{"spot", "linear"},
				Instruments: Refresh{RefreshInterval: 10 * time.Minute},
				Klines:      PageLimits{MaxCandlesPerRequest: Markets[int]{Spot: BinanceSpotKlineMaximum, Linear: BinanceLinearKlineMaximum}},
				MarketStats: Refresh{RefreshInterval: 30 * time.Second},
			},
		},
		HTTPClient: HTTPClient{
			Timeout:          10 * time.Second,
			MaxResponseBytes: 16777216,
			Retry:            Retry{MaxAttempts: 3, InitialBackoff: 100 * time.Millisecond, MaxBackoff: 2 * time.Second},
		},
		Observability: Observability{
			Sentry:     Sentry{Environment: "local", TracesSampleRate: 0.1},
			Prometheus: Prometheus{Path: "/metrics"},
			Stats:      Stats{Enabled: true},
		},
	}
}
