.PHONY: build test test-race cover vet prek lint run clean tidy help

GO ?= go
BINARY := a10r

VERSION ?= dev
# Deferred (=) so subshells run only for recipes that actually need them.
# Note: DATE re-evaluates per recipe invocation, so two builds in the
# same `make` chain may carry different timestamps. Reproducible
# release builds go through goreleaser, which pins the date once.
COMMIT   = $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -s -w \
	-X github.com/wilfriedroset/a10r/cmd.version=$(VERSION) \
	-X github.com/wilfriedroset/a10r/cmd.commit=$(COMMIT) \
	-X github.com/wilfriedroset/a10r/cmd.date=$(DATE)

build: ## Build the a10r binary
	$(GO) build -trimpath -ldflags='$(LDFLAGS)' -o $(BINARY) .

test: ## Run unit tests
	$(GO) test ./...

test-race: ## Run unit tests with the race detector
	$(GO) test -race ./...

cover: ## Run unit tests with coverage profile (requires covdata; use CI Go toolchain)
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

vet: ## Run go vet
	$(GO) vet ./...

prek: ## Run all pre-commit hooks (canonical lint entrypoint)
	prek run --all-files

lint: prek ## Alias for `prek`

run: build ## Build and run the binary
	./$(BINARY)

tidy: ## Tidy module dependencies
	$(GO) mod tidy

clean: ## Remove build artifacts
	rm -f $(BINARY) coverage.out
	rm -rf dist/

help: ## Show this help message
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
