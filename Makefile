VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
# The official build includes the paid features (files tagged "pro"); the
# open-source tree builds without them.
TAGS ?= pro

.PHONY: build build-free license-tool test e2e price-audit release public

build:
	go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o bin/costhound .

build-free:
	go build -ldflags "$(LDFLAGS)" -o bin/costhound-free .

# The seller's license tool (never exported: it signs with the seller's key).
license-tool:
	@test -d cmd/costhound-license || { echo "the seller's license tool isn't part of the open-source tree" >&2; exit 1; }
	go build -tags pro -o bin/costhound-license ./cmd/costhound-license

# Both builds must compile, vet and pass their tests.
test:
	go vet ./...
	go test ./...
	go vet -tags pro ./...
	go test -tags pro ./...

# End-to-end run of the real binary against a local AWS emulator (moto).
# Never touches real AWS.
e2e: build
	scripts/e2e-moto.sh

# Opt-in live Azure Retail Prices audit (network). Not part of test/e2e.
price-audit:
	COSTHOUND_LIVE_PRICES=1 go test -tags pro -run TestLivePriceAudit -count=1 -v ./internal/azure/

# Cross-compiled official binaries + checksums in dist/.
release: test
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%%/*}; arch=$${p##*/}; ext=; [ $$os = windows ] && ext=.exe; \
		out=dist/costhound_$(VERSION)_$${os}_$${arch}$$ext; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -tags "$(TAGS)" -trimpath -ldflags "$(LDFLAGS)" -o $$out . && echo $$out; \
	done
	@cd dist && sha256sum costhound_$(VERSION)_* > SHA256SUMS

# Export the open-source tree (no paid code) to PUBLIC_DIR and prove it builds.
PUBLIC_DIR ?= ../costhound-public
public:
	scripts/export-public.sh $(PUBLIC_DIR)
