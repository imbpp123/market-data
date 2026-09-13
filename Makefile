GO ?= go
export GOTOOLCHAIN := go1.27.1

GOLANGCI_LINT_VERSION := v2.13.2
GOLANGCI_LINT_DIR := $(CURDIR)/bin/golangci-lint/$(GOLANGCI_LINT_VERSION)
GOLANGCI_LINT := $(GOLANGCI_LINT_DIR)/golangci-lint

.PHONY: build run fmt-check check-config test test-race vet install-lint lint check docker-build docker-up docker-down docker-verify release-load
build:
	$(GO) build -o bin/market-data-service ./cmd/market-data-service

run:
	$(GO) run ./cmd/market-data-service -config config/config-v1.yaml

fmt-check:
	@files=$$(gofmt -l cmd internal scripts/api/httpfixture scripts/api/grpcfixture api/go) || exit $$?; \
	if [ -n "$$files" ]; then \
		printf 'Go files need formatting:\n%s\n' "$$files"; \
		exit 1; \
	fi

check-config: build
	./bin/market-data-service -config config/config-v1.yaml -check-config

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

docker-build:
	docker compose build

docker-up:
	docker compose up -d --wait

docker-down:
	docker compose down

docker-verify:
	CGO_ENABLED=0 GOOS=linux $(GO) test -c -o bin/release-probe ./internal/bootstrap
	python3 scripts/verify-container.py

release-load:
	MDS_RELEASE_LOAD=main GOMEMLIMIT=700MiB $(GO) test ./internal/bootstrap -run '^TestReleaseLoad$$' -count=1 -v -timeout=15m

install-lint: $(GOLANGCI_LINT)

# Install the official binary once per pinned version, outside the Go module.
$(GOLANGCI_LINT):
	mkdir -p "$(GOLANGCI_LINT_DIR)"
	curl -fsSL --connect-timeout 10 --max-time 60 \
		"https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_VERSION)/install.sh" \
		-o "$(GOLANGCI_LINT_DIR)/install.sh"
	sh "$(GOLANGCI_LINT_DIR)/install.sh" -b "$(GOLANGCI_LINT_DIR)" "$(GOLANGCI_LINT_VERSION)"

lint: $(GOLANGCI_LINT)
	"$(GOLANGCI_LINT)" config verify --config .golangci.yml
	"$(GOLANGCI_LINT)" run --config .golangci.yml ./...

# Separate recursive calls preserve this fail-fast order even with make -j.
# check-config builds the binary first; govet is part of the standard linter set.
check:
	$(MAKE) fmt-check
	$(MAKE) check-config
	$(MAKE) lint
	$(MAKE) test
	$(MAKE) test-race

# Client tooling is isolated from normal service builds.
API_PYTHON ?= python3.13
API_VENV := $(CURDIR)/bin/api-tools
API_GO_TOOLS := $(CURDIR)/bin/api-tools-go
export BUF_CACHE_DIR := $(CURDIR)/bin/buf-cache

.PHONY: install-api-tools generate-api check-api api-http-baseline
install-api-tools: $(API_VENV)/.installed $(API_GO_TOOLS)/protoc-gen-go $(API_GO_TOOLS)/protoc-gen-go-grpc $(API_GO_TOOLS)/buf

$(API_VENV)/.installed: scripts/api/requirements.txt
	$(API_PYTHON) -m venv "$(API_VENV)"
	"$(API_VENV)/bin/python" -m pip install -r scripts/api/requirements.txt
	touch "$@"

$(API_GO_TOOLS)/protoc-gen-go:
	GOBIN="$(API_GO_TOOLS)" $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.10

$(API_GO_TOOLS)/protoc-gen-go-grpc:
	GOBIN="$(API_GO_TOOLS)" $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

$(API_GO_TOOLS)/buf:
	GOBIN="$(API_GO_TOOLS)" $(GO) install github.com/bufbuild/buf/cmd/buf@v1.59.0

generate-api: install-api-tools
	"$(API_VENV)/bin/python" scripts/api/generate.py

check-api: install-api-tools
	"$(API_VENV)/bin/python" scripts/api/generate.py --check
	@test "$$("$(API_GO_TOOLS)/buf" --version)" = "1.59.0"
	"$(API_GO_TOOLS)/buf" breaking api/descriptor.binpb --against api/compatibility/baseline.binpb --config api/compatibility/buf.yaml
	"$(API_VENV)/bin/python" scripts/api/test_generation.py
	"$(API_VENV)/bin/python" scripts/api/test_baseline.py
	"$(API_VENV)/bin/python" scripts/api/test_comparison.py
	cd api/go && $(GO) build ./... && $(GO) vet ./... && $(GO) test ./... && $(GO) test -race ./...
	"$(API_VENV)/bin/python" scripts/api/check_package.py

api-http-baseline: install-api-tools
	"$(API_VENV)/bin/python" scripts/api/http_baseline.py
