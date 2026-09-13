module market-data

go 1.27.1

require (
	github.com/andybalholm/brotli v1.2.3
	github.com/binance/binance-connector-go/clients/derivativestradingusdsfutures v1.20.2
	github.com/binance/binance-connector-go/clients/spot v1.14.2-0.20260910112333-9803fd1c7ed7
	github.com/binance/binance-connector-go/common/v2 v2.8.1
	github.com/bybit-exchange/bybit.go.api v1.1.2-0.20260724042240-a58e14c6fd93
	github.com/getsentry/sentry-go v0.48.0
	github.com/go-viper/mapstructure/v2 v2.4.0
	github.com/imbpp123/market-data/api/go v0.0.0
	github.com/knadh/koanf/providers/confmap v1.0.0
	github.com/knadh/koanf/providers/env/v2 v2.0.0
	github.com/knadh/koanf/v2 v2.3.0
	github.com/shopspring/decimal v1.4.0
	github.com/stretchr/testify v1.11.1
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/sync v0.23.0
	google.golang.org/grpc v1.76.0
	google.golang.org/protobuf v1.36.10
)

require (
	github.com/bitly/go-simplejson v0.5.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/knadh/koanf/maps v0.1.2 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250804133106-a7a43d27e69b // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/imbpp123/market-data/api/go => ./api/go
