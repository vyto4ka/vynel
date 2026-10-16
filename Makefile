VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/vyto4ka/vynel/internal/buildinfo.Version=$(VERSION) \
	-X github.com/vyto4ka/vynel/internal/buildinfo.Commit=$(COMMIT) \
	-X github.com/vyto4ka/vynel/internal/buildinfo.Date=$(DATE)

XRAY_DIR ?= $(CURDIR)/.cache/xray
CADDY_DIR ?= $(CURDIR)/.cache/caddy
PEBBLE_DIR ?= $(CURDIR)/.cache/pebble

.PHONY: build build-all test test-integration lint proto web xray caddy pebble clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vynel ./cmd/vynel

build-all:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vynel-linux-amd64 ./cmd/vynel
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vynel-linux-arm64 ./cmd/vynel

test:
	go test ./...

# Integration tests run real Xray, nginx (apt install nginx libnginx-mod-stream), Pebble for
# ACME and Caddy for agents of older panels (`make xray caddy pebble`).
test-integration: xray caddy pebble
	XRAY_BIN=$(XRAY_DIR)/xray XRAY_LOCATION_ASSET=$(XRAY_DIR) CADDY_BIN=$(CADDY_DIR)/caddy PEBBLE_BIN=$(PEBBLE_DIR)/pebble go test -count=1 ./...

lint:
	golangci-lint run ./...

proto:
	PATH="$$PATH:$$(go env GOPATH)/bin" buf generate

web:
	cd web && pnpm install --frozen-lockfile && pnpm build && touch dist/.gitkeep

xray:
	./scripts/fetch-xray.sh $(XRAY_DIR)

# Pebble, Let's Encrypt's ACME test server, built with the Go toolchain.
pebble:
	@test -x $(PEBBLE_DIR)/pebble || GOBIN=$(PEBBLE_DIR) go install github.com/letsencrypt/pebble/v2/cmd/pebble@v2.7.0

# Caddy built from source with the Go toolchain (no extra download hosts needed).
caddy:
	@test -x $(CADDY_DIR)/caddy || GOBIN=$(CADDY_DIR) go install github.com/caddyserver/caddy/v2/cmd/caddy@latest

clean:
	rm -rf bin .cache
