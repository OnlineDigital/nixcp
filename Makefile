GO ?= go
NIXPKGS_REF ?= nixpkgs
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuiltAt=$(BUILD_DATE)

.PHONY: all fmt fmt-check vet test check race fuzz-smoke build static test-shell nix-eval nix-container-test completions release clean

all: check build

fmt:
	$(GO) fmt ./...

fmt-check:
	@test -z "$$($(GO)fmt -l .)" || (echo "gofmt required:"; $(GO)fmt -l .; exit 1)

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

check: fmt-check vet test

race:
	$(GO) test -race ./internal/state ./internal/transaction

fuzz-smoke:
	$(GO) test ./internal/nix -run '^$$' -fuzz '^FuzzNixString$$' -fuzztime=2s
	$(GO) test ./internal/php -run '^$$' -fuzz '^FuzzParseMarker$$' -fuzztime=2s

build:
	mkdir -p build
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o build/ncp ./cmd/ncp

static:
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo 'staticcheck unavailable; install honnef.co/go/tools/cmd/staticcheck'; fi
	@if command -v govulncheck >/dev/null 2>&1; then govulncheck ./...; else echo 'govulncheck unavailable; install golang.org/x/vuln/cmd/govulncheck'; fi

test-shell:
	$(GO) test ./internal/command -run 'TestShell' -count=1

nix-eval:
	@if command -v nix >/dev/null 2>&1; then nix eval --impure --expr 'let pkgs = import <$(NIXPKGS_REF)> {}; in pkgs.stdenv.hostPlatform.system'; else echo 'nix unavailable; Nix evaluation skipped'; fi

nix-container-test:
	./scripts/test-nix-container.sh

completions: build
	mkdir -p dist/completions
	./build/ncp completion bash > dist/completions/ncp.bash
	./build/ncp completion zsh > dist/completions/_ncp
	./build/ncp completion fish > dist/completions/ncp.fish

release: check race fuzz-smoke build completions
	mkdir -p dist/package
	cp build/ncp README.md SECURITY.md CHANGELOG.md dist/package/
	cp -R dist/completions dist/package/completions
	tar -C dist/package -czf dist/ncp_$(VERSION)_linux_amd64.tar.gz .
	sha256sum dist/ncp_$(VERSION)_linux_amd64.tar.gz > dist/ncp_$(VERSION)_linux_amd64.tar.gz.sha256
	@echo 'Release archive and checksum created. Generate SBOM/provenance in CI.'

clean:
	rm -rf build dist
