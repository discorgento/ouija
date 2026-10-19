# ouija — build/run/test shortcuts.
# No dependencies beyond `make` and `go`. Discover everything with: make help
#
# Conventions:
#   - Binaries land at the REPO ROOT (/ouija, /ouija-mcp), both gitignored.
#     Never use a bare `ouija` pattern: `cmd/ouija/` is the source directory.
#   - Every target accepts arguments: `make run ARGS='task "x"'`.
#   - Version: the single source is the git TAG, created automatically by
#     .github/workflows/release.yml — never edit a version anywhere. One-off
#     override: `make build VERSION=vX.Y.Z`. With no VERSION and no tag, the
#     fallback in internal/version applies; ldflags is never passed empty.

GO       ?= go
BIN      ?= ./ouija
BIN_MCP  ?= ./ouija-mcp
PACKAGES  = ./...
ARGS     ?=

VERSION  ?= $(shell git describe --tags --dirty 2>/dev/null)
# Tags are `vX.Y.Z`; the binary prints the bare semver (`0.0.1`). The `v`
# prefix is stripped both for a git tag and for `VERSION=vX.Y.Z` passed on the
# command line.
VERSION_BARE = $(patsubst v%,%,$(VERSION))
LDFLAGS   = $(if $(VERSION_BARE),-ldflags "-X github.com/discorgento/ouija/internal/version.Version=$(VERSION_BARE)")

DIST      = dist
PLATFORMS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.DEFAULT_GOAL := help
.PHONY: help build build-mcp dist install run chat task setup config learn \
        test test-race vet fmt fmt-check check tidy cover clean

help: ## List every target
	@grep -E '^[a-zA-Z][a-zA-Z0-9_-]*:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-11s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Pass arguments:  make run ARGS='task \"your task\"'"
	@echo "Version:         make build VERSION=vX.Y.Z  (git tag is the default)"
	@echo "Release:         make dist  (5 targets → dist/*.tar.gz + SHA256SUMS)"

build: ## Build ouija and ouija-mcp at the repo root
	$(GO) build $(LDFLAGS) -o $(BIN) ./cmd/ouija
	$(GO) build $(LDFLAGS) -o $(BIN_MCP) ./cmd/ouija-mcp
	@echo "ok → $(BIN), $(BIN_MCP)"

build-mcp: ## Build only the MCP server
	$(GO) build $(LDFLAGS) -o $(BIN_MCP) ./cmd/ouija-mcp

dist: ## Cross-compile 5 targets (linux/darwin/windows) → dist/*.tar.gz + SHA256SUMS
	rm -rf $(DIST) && mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=""; \
	  if [ "$$os" = windows ]; then ext=".exe"; fi; \
	  out=$(DIST)/ouija-$$os-$$arch; mkdir -p $$out; \
	  echo "→ $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $$out/ouija$$ext ./cmd/ouija; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $$out/ouija-mcp$$ext ./cmd/ouija-mcp; \
	done
	@cd $(DIST) && for d in ouija-*/; do d=$${d%/}; tar -czf "$$d.tar.gz" "$$d" && rm -rf "$$d"; done
	@cd $(DIST) && if command -v sha256sum >/dev/null 2>&1; then \
	  sha256sum *.tar.gz > SHA256SUMS; \
	else \
	  shasum -a 256 *.tar.gz > SHA256SUMS; \
	fi; echo "ok → $(DIST)/SHA256SUMS"
	@echo "ok →"; ls -lh $(DIST) | tail -n +2

install: ## Install both binaries into $$GOBIN/$$GOPATH/bin (idempotent: skips identical versions)
	@d="$$($(GO) env GOBIN)"; [ -n "$$d" ] || d="$$($(GO) env GOPATH)/bin"; \
	version=$$($$d/ouija version 2>/dev/null || echo "none"); \
	src_version=$$($(GO) run -ldflags "$(LDFLAGS)" ./cmd/ouija version 2>/dev/null); \
	if [ "$$version" = "$$src_version" ]; then \
	  echo "🔮  $$version already installed in $$d (idempotent)"; \
	else \
	  echo "🔧  installing $$src_version (current: $$version)"; \
	  $(GO) install $(LDFLAGS) ./cmd/ouija ./cmd/ouija-mcp; \
	  v=$$($$d/ouija version 2>/dev/null || echo "$$src_version"); \
	  echo "🔮  $$v installed in $$d"; \
	fi; \
	case ":$$PATH:" in *":$$d:"*) echo "✓  $$d already on PATH" ;; \
	*) echo "→  export PATH=\"$$d:\$$PATH\"  # add it to ~/.zshrc / ~/.bashrc" ;; esac

run: build ## Build and run with ARGS (default: show help)
	$(BIN) $(ARGS)

chat: build ## Build and open the chat TUI in the current directory
	$(BIN) chat $(ARGS)

task: build ## Build and run the task totem with ARGS (e.g. make task ARGS='"refactor X"')
	$(BIN) task $(ARGS)

setup: build ## Build and run the setup assistant
	$(BIN) setup $(ARGS)

config: build ## Build and run the provider/model selector
	$(BIN) config $(ARGS)

learn: build ## Build and save knowledge: make learn ARGS='"fact"'
	$(BIN) learn $(ARGS)

test: ## Run every test
	$(GO) test $(PACKAGES)

test-race: ## Run tests with the race detector (slower)
	$(GO) test -race $(PACKAGES)

vet: ## Static analysis
	$(GO) vet $(PACKAGES)

fmt: ## Rewrite code with gofmt -w
	gofmt -w .

fmt-check: ## Fail on any file outside the gofmt standard (read-only)
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then \
	  echo "outside gofmt standard:"; echo "$$out"; exit 1; \
	fi; echo "gofmt: ok"

check: fmt-check vet test ## Full gate: gofmt + vet + tests (run before committing)
	@echo "check: ok"

tidy: ## Sync go.mod/go.sum with the imports
	$(GO) mod tidy

upgrade: ## Update deps (go get -u), tidy and reinstall
	$(GO) get -u ./...
	$(GO) mod tidy
	$(MAKE) install

cover: ## Generate a coverage report (coverage.html)
	$(GO) test -coverprofile=coverage.out $(PACKAGES)
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "ok → coverage.html"

clean: ## Remove binaries and release artifacts
	rm -f $(BIN) $(BIN_MCP) coverage.out coverage.html
	rm -rf $(DIST)
