GO ?= go
export GOTOOLCHAIN := go1.27.1

GOLANGCI_LINT_VERSION := v2.13.2
GOLANGCI_LINT_DIR := $(CURDIR)/bin/golangci-lint/$(GOLANGCI_LINT_VERSION)
GOLANGCI_LINT := $(GOLANGCI_LINT_DIR)/golangci-lint

.PHONY: build run fmt-check check-config test test-race vet install-lint lint check docker-build docker-up docker-down docker-verify release-load
build:
	$(GO) build -o bin/market-data-service ./cmd/market-data-service

run:
	$(GO) run ./cmd/market-data-service -config docs/examples/config-v1.yaml

fmt-check:
	@files=$$(gofmt -l cmd internal) || exit $$?; \
	if [ -n "$$files" ]; then \
		printf 'Go files need formatting:\n%s\n' "$$files"; \
		exit 1; \
	fi

check-config: build
	./bin/market-data-service -config docs/examples/config-v1.yaml -check-config

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
