APP_NAME    := kilasflow
GO          := go
WEB_DIR     := web
DIST_DIR    := internal/web/dist
BIN_DIR     := bin
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
LDFLAGS     := -s -w -X main.version=$(VERSION)
GO_BIN      := $(shell $(GO) env GOBIN 2>/dev/null)

ifeq ($(strip $(GO_BIN)),)
GO_BIN      := $(shell $(GO) env GOPATH 2>/dev/null)/bin
endif

# `go install` writes Air to GOBIN (or GOPATH/bin), which need not be on a
# developer's shell PATH. Keep the override for a system-managed Air binary.
AIR         ?= $(GO_BIN)/air

# The frontend build wipes DIST_DIR, but internal/web/embed.go embeds it
# unconditionally. Without a file in place `go build`, `go vet` and `go test`
# all fail, so every Go target depends on the placeholder being restored.
PLACEHOLDER := $(DIST_DIR)/index.html

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@echo "KilasFlow development commands"
	@echo ""
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Dev servers:  Go on :8080, Vite on :5173 (open :5173)"

.PHONY: setup
setup: ## Install all dependencies
	$(GO) mod download
	$(GO) install github.com/air-verse/air@v1.67.4
	cd $(WEB_DIR) && pnpm install

# Override when a port is taken by another project:
#   make dev KILASFLOW_WEB_PORT=5180
KILASFLOW_WEB_PORT ?= 5173
export KILASFLOW_WEB_PORT

.PHONY: dev
dev: dist-placeholder ## Run backend and frontend with hot reload
	@echo "Go   -> http://127.0.0.1:8080"
	@echo "Vite -> http://127.0.0.1:$(KILASFLOW_WEB_PORT)   <- open this one"
	@trap 'kill 0' INT TERM EXIT; \
		$(MAKE) dev-api & api_pid=$$!; \
		$(MAKE) dev-web & web_pid=$$!; \
		while kill -0 "$$api_pid" 2>/dev/null && kill -0 "$$web_pid" 2>/dev/null; do sleep 1; done; \
		exit 1

.PHONY: dev-api
dev-api: dist-placeholder ## Run the Go backend with Air
	$(AIR)

.PHONY: dev-web
dev-web: ## Run the SvelteKit dev server
	cd $(WEB_DIR) && pnpm dev

.PHONY: dist-placeholder
dist-placeholder:
	@mkdir -p $(DIST_DIR)
	@touch $(DIST_DIR)/.gitkeep
	@test -f $(PLACEHOLDER) || git checkout -- $(PLACEHOLDER) 2>/dev/null \
		|| printf '<!doctype html><title>KilasFlow</title><p>SPA not built. Run <code>make build-web</code>.</p>\n' > $(PLACEHOLDER)

.PHONY: build-web
build-web: ## Build the SPA into the Go embed directory
	cd $(WEB_DIR) && pnpm build
	rm -rf $(DIST_DIR)
	mkdir -p $(DIST_DIR)
	cp -R $(WEB_DIR)/build/. $(DIST_DIR)/
	@touch $(DIST_DIR)/.gitkeep

.PHONY: build
build: dist-placeholder ## Build the production binary (run build-web first for the real SPA)
	CGO_ENABLED=0 $(GO) build \
		-trimpath \
		-ldflags="$(LDFLAGS)" \
		-o $(BIN_DIR)/$(APP_NAME) \
		./cmd/$(APP_NAME)
	@echo "built $(BIN_DIR)/$(APP_NAME) ($(VERSION))"

.PHONY: build-all
build-all: build-web build ## Full production build: SPA embedded in the binary

.PHONY: run
run: build ## Build and run the binary
	./$(BIN_DIR)/$(APP_NAME)

.PHONY: test
test: dist-placeholder ## Run Go tests
	$(GO) test ./... -race

.PHONY: test-cover
test-cover: dist-placeholder ## Run Go tests with a coverage report
	$(GO) test ./... -coverprofile=coverage.out
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: dist-placeholder ## Vet Go code and typecheck the frontend
	$(GO) vet ./...
	@test -z "$$(gofmt -l . | grep -v '^$(WEB_DIR)/')" || \
		{ echo "gofmt needed:"; gofmt -l . | grep -v '^$(WEB_DIR)/'; exit 1; }
	cd $(WEB_DIR) && pnpm check

.PHONY: tidy
tidy: ## Tidy go.mod
	$(GO) mod tidy

.PHONY: docker
docker: ## Build the Docker image
	docker build -t $(APP_NAME):$(VERSION) -t $(APP_NAME):latest .

.PHONY: corpus
corpus: ## Fetch the n8n importer regression corpus (needs KILASFLOW_N8N_REFERENCE)
	scripts/corpus-sync.sh

.PHONY: corpus-baseline
corpus-baseline: ## Rescore the corpus and rewrite BASELINE.md and baseline.json
	$(GO) test ./internal/interop/n8n/corpus -update-baseline -count=1 -v

.PHONY: smoke-sqlite
smoke-sqlite: ## Prove the embedded binary against a fresh SQLite database
	sh scripts/smoke-sqlite.sh

.PHONY: smoke-dev
smoke-dev: ## Prove the Vite development proxy against a temporary Go server
	sh scripts/smoke-dev.sh

.PHONY: smoke-docker
smoke-docker: ## Prove the non-root Docker image against a persisted SQLite bind mount
	sh scripts/smoke-docker.sh

.PHONY: smoke-postgres
smoke-postgres: ## Prove the Docker image against the temporary Compose PostgreSQL service
	sh scripts/smoke-postgres.sh

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) .tmp coverage.out
	rm -rf $(WEB_DIR)/build $(WEB_DIR)/.svelte-kit
	rm -rf $(DIST_DIR)
	@$(MAKE) --no-print-directory dist-placeholder
