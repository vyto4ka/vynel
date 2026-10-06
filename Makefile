VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/vyto4ka/vpn/internal/buildinfo.Version=$(VERSION) \
	-X github.com/vyto4ka/vpn/internal/buildinfo.Commit=$(COMMIT) \
	-X github.com/vyto4ka/vpn/internal/buildinfo.Date=$(DATE)

XRAY_DIR ?= $(CURDIR)/.cache/xray

.PHONY: build build-all test test-integration lint proto web xray clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vpn ./cmd/vpn

build-all:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vpn-linux-amd64 ./cmd/vpn
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vpn-linux-arm64 ./cmd/vpn

test:
	go test ./...

# Integration tests run a real Xray binary (downloaded by `make xray`).
test-integration: xray
	XRAY_BIN=$(XRAY_DIR)/xray XRAY_LOCATION_ASSET=$(XRAY_DIR) go test -count=1 ./...

lint:
	golangci-lint run ./...

proto:
	PATH="$$PATH:$$(go env GOPATH)/bin" buf generate

web:
	cd web && pnpm install --frozen-lockfile && pnpm build && touch dist/.gitkeep

xray:
	./scripts/fetch-xray.sh $(XRAY_DIR)

clean:
	rm -rf bin .cache
