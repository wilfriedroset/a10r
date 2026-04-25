.PHONY: build test test-race cover vet prek lint run clean tidy am-up am-down am-logs smoke help

GO ?= go
BINARY := a10r

# Local Alertmanager smoke environment (per N1's vanilla floor v0.28.1).
# Runs in docker on 127.0.0.1:9093 with a minimal one-receiver config
# from examples/alertmanager.yml.
AM_VERSION   := v0.28.1
AM_CONTAINER := a10r-am-test
AM_CONFIG    := $(abspath examples/alertmanager.yml)
AM_PORT      := 9093

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

am-up: ## Start a local Alertmanager v0.28.1 at http://127.0.0.1:$(AM_PORT)
	@docker rm -f $(AM_CONTAINER) >/dev/null 2>&1 || true
	@echo "Starting prom/alertmanager:$(AM_VERSION) on 127.0.0.1:$(AM_PORT)..."
	@docker run -d --rm \
		--name $(AM_CONTAINER) \
		-p 127.0.0.1:$(AM_PORT):9093 \
		-v $(AM_CONFIG):/etc/alertmanager/alertmanager.yml:ro \
		prom/alertmanager:$(AM_VERSION) \
		--config.file=/etc/alertmanager/alertmanager.yml \
		--web.listen-address=:9093 \
		--cluster.listen-address= >/dev/null
	@echo -n "Waiting for Alertmanager to come up"; \
	for i in $$(seq 1 20); do \
		if curl -sf http://127.0.0.1:$(AM_PORT)/api/v2/status >/dev/null 2>&1; then \
			echo " ready."; exit 0; \
		fi; \
		echo -n "."; sleep 1; \
	done; \
	echo " timed out waiting for /api/v2/status"; \
	docker logs --tail 30 $(AM_CONTAINER); \
	exit 1

am-down: ## Stop the local Alertmanager
	@docker rm -f $(AM_CONTAINER) >/dev/null 2>&1 && echo "Stopped." || echo "Not running."

am-logs: ## Tail the local Alertmanager logs
	@docker logs -f $(AM_CONTAINER)

smoke: ## Run the backend smoke harness against the local Alertmanager
	$(GO) run ./cmd/smoke -url http://127.0.0.1:$(AM_PORT)

help: ## Show this help message
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
