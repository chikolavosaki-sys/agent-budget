## agent-budget — common commands. Run `make help` to see them all.

BIN_DIR := bin
GOLANGCI_LINT ?= golangci-lint
# Agents file for `make run-proxy`. Override: make run-proxy AGENTS_FILE=my-agents.json
AGENTS_FILE ?= examples/agents.json

.PHONY: help build test cover lint fmt run-fake run-proxy keygen clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build all binaries into bin/
	go build -o $(BIN_DIR)/ ./cmd/...

test: ## Run unit tests with the race detector
	go test -race ./...

cover: ## Run tests and open a coverage report
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run

fmt: ## Format all Go code
	gofmt -w .

run-fake: build ## Start the fake LLM on :9090
	./$(BIN_DIR)/fakellm

run-proxy: build ## Start the proxy on :8080 in front of the fake LLM, with the demo agents
	UPSTREAM_URL=http://localhost:9090 AGENTS_FILE="$(AGENTS_FILE)" ./$(BIN_DIR)/proxy

keygen: build ## Print a new virtual key and its hash for the agents file
	./$(BIN_DIR)/proxy keygen

clean: ## Remove build output
	rm -rf $(BIN_DIR) coverage.out
