---
id: BUG-8sb0jw
title: 'Build/CI/release hygiene: red CI, shared PG tests, skipped Playwright, dirty builds, stale client, docs'
status: doing
priority: medium
labels:
    - dx
    - ci
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T14:24:35Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 11 finding(s) from dims: find:build-health-dx.

---
### build-web overwrites the tracked SPA placeholder: builds report "-dirty", and the built index.html has already been committed, breaking fresh-clone builds [find:build-health-dx] (medium/bug) · area: build / Makefile / version string · confidence: high

internal/web/dist/index.html is tracked as the "SPA not built" placeholder, but `make build-web` (and therefore build-all and the e2e global setup) replaces it. The tree becomes dirty, VERSION is computed afterwards, and every build is stamped -dirty. The built file was committed in 75d0441 and bf802ab (HEAD), so a fresh clone plus `go build` now embeds an index.html whose hashed JS modules do not exist.

Evidence: Makefile:112-118 (rm -rf dist + cp web/build), VERSION from `git describe --dirty` exported per target, and `make -n build-all` shows build-web running before go build. The shared review binary reports version `bf802ab-dirty` (/api/v1/health) although HEAD is clean; local docker images are tagged kilasflow:4842198-dirty, 08440c4-dirty and 94a487d-dirty. `git show HEAD:internal/web/dist/index.html` references /_app/immutable/entry/start.4yiz8tni.js and other files that are not tracked. I built from `git archive HEAD` in scratch and ran it on :8117: /app/workflows serves that index.html, and GET /_app/immutable/entry/start.4yiz8tni.js returns 200 text/html (SPA fallback), so the editor is blank with MIME errors instead of the placeholder. dist-placeholder (`test -f index.html || git checkout`) keeps the stale file. e2e/global-setup.ts runs `make build-all` inside the checkout.

Impact: Development and CI builds cannot produce a clean version string. Anyone building from a fresh clone without pnpm gets a broken SPA shell. (The committed file is also reported by unfinished-work F3.)

Suggested fix: Restore the placeholder and stop tracking a file that the build rewrites: embed a fallback from a separate path, or build into an untracked dir. Compute VERSION before build-web or exclude internal/web/dist from the dirty check. Add a CI check that dist/index.html equals the placeholder.

Files: Makefile, internal/web/dist/index.html, .gitignore, e2e/global-setup.ts

---
### Postgres-gated Go tests share one database across packages and interfere with each other under parallel `go test ./...` [find:build-health-dx] (medium/dx) · area: tests / CI · confidence: high

The integration tests migrate, drop and roll back tables in the single database named by KILASFLOW_TEST_POSTGRES_DSN. With packages running in parallel, as `make test` does, tests in one package destroy another package's schema. The same packages pass when run one at a time.

Evidence: KILASFLOW_TEST_POSTGRES_DSN pointed at my own pgvector container, `go test -p 4 ./internal/database ./internal/engine ./internal/repository ./internal/loadoptions ./internal/sqlbuild ./nodes`. internal/engine fails with `apply migration 000002_workflow_history: relation "workflow_versions" does not exist`, `read applied migrations: relation "schema_migrations" does not exist`, worker `relation "executions" does not exist`, and TestAbandonedLeaseIsReclaimed... RunOnce=false. internal/repository fails with `cached plan must not change result type (SQLSTATE 0A000)`. nodes TestVectorSearchUsesTheANNIndex gets a btree plan. Re-run with -p 1: database, engine and repository all ok, and the ANN test passes. Example culprit: internal/database TestRollingBackEveryPostgresMigrationLeavesNoKilasFlowTables drops every table in the shared DB. CI runs `go test ./... -race` against one DSN.

Impact: Once the pgvector CI failure is fixed, the Postgres suite will be flaky, which erodes trust in the only multi-worker and queue coverage.

Suggested fix: Give each test binary (or test) its own schema with search_path, or its own database via CREATE DATABASE, derived from the DSN. Alternatively run the Postgres packages with -p 1 in CI.

Files: internal/database/migrate_test.go, internal/engine/multiprocess_test.go, internal/repository/claim_wake_test.go, nodes/pgvector_test.go

---
### 27% of the Playwright suite never runs in CI (26/97 skipped), including every PostgreSQL, WAHA-migration, local-model and live-n8n proof [find:build-health-dx] (medium/dx) · area: e2e / CI · confidence: high

The e2e CI job has no database service, no corpus, no model stub and no n8n credentials, so the env-gated specs skip every time and a skip reads as a pass. The epic acceptance suite therefore proves only 3 of its 7 scenarios in CI.

Evidence: Job log for CI run 34571965928, "End-to-end (Playwright)": "26 skipped / 71 passed"; retries are 2 in CI (e2e/playwright.config.ts:24). Skipped: tests/datastore-pg.spec.ts (3) and epic proof 3 on postgres (no KILASFLOW_TEST_POSTGRES_DSN, fixtures/datastore.ts:422); tests/waha-migration.spec.ts (4) and epic proof 2 (.corpus absent, `make corpus` needs KILASFLOW_N8N_REFERENCE); tests/ai-agent-ollama.spec.ts (6) and epic proof 1 (needs local Ollama); n8n-compare and library-import live cases. e2e/global-setup.ts runs `make build-all` in the working tree, so the suite cannot run without modifying the checkout (see the build-web finding).

Impact: The pieces most tied to V2 goals (Postgres datastore, WAHA n8n migration, agent path) have no automated regression protection.

Suggested fix: Add a pgvector service and DSN to the e2e job. Ship a licence-clean, self-authored WAHA fixture so the migration proof runs. Add an OpenAI-compatible stub model for the agent proofs. Have global-setup build into a temp dir. Surface the skip count and fail if it grows.

Files: .github/workflows/ci.yml, e2e/playwright.config.ts, e2e/global-setup.ts, e2e/tests/datastore-pg.spec.ts

Existing tickets: FEAT-5fhj6p

---
### The embedded SPA is served without compression or cache headers, and missing hashed assets fall back to index.html with status 200 [find:build-health-dx] (medium/perf) · area: internal/web static serving · confidence: high

http.FileServerFS over embed.FS gives no Last-Modified or ETag (zero modtime), no Cache-Control and no gzip/br. Every editor load, including every embedded iframe load inside a host product, re-downloads all JS uncompressed. Unknown /_app/immutable paths return index.html as 200 text/html.

Evidence: internal/web/embed.go Handler. On the shared server, GET /_app/immutable/entry/start.*.js with `Accept-Encoding: gzip, br` returns no Content-Encoding, Cache-Control, ETag or Last-Modified. Editor JS is 772,651 bytes raw vs 231,983 bytes gzip -9 (web/build/_app). /vendor/scalar.js for /docs is 3,736,898 bytes uncompressed. A missing chunk returns 200 text/html (seen on the fresh-clone binary).

Impact: Slow editor loads, especially embedded or over WAN. After an upgrade, stale tabs fail with MIME errors instead of a recoverable 404.

Suggested fix: Serve /_app/immutable/* with `Cache-Control: public, max-age=31536000, immutable` and index.html with no-cache. Precompress at build time or add gzip middleware. Return 404 for missing files under /_app/ and other asset-like paths.

Files: internal/web/embed.go

---
### Operator docs contradict the shipped product: PostgreSQL image, migration count, internal-DB guard status, CLI flags [find:build-health-dx] (medium/docs) · area: docs/operate + compose comments · confidence: high

Several operator-facing statements are stale. One of them (restore into postgres:17-alpine) leads to a failed restore.

Evidence: (1) operate/upgrades.md:48-50 says to restore into the same major because "the service pins `postgres:17-alpine`". compose.postgres.yaml actually pins pgvector/pgvector:pg17, and restoring a dump into postgres:17-alpine fails on CREATE EXTENSION vector. (2) start/install.md:167-168 says "the same three migrations applied"; the binary applies 8 (1-6, 8, 9; firstrun/server.log). (3) operate/security.md:49-53 says "On PostgreSQL there is no guard yet ... FEAT-a94c8y closes this", and operate/deployment.md:65-67,151-153 says SQL nodes do no host validation. FEAT-a94c8y is done, and cmd/kilasflow/main.go:819-829 builds the postgres internal-target guard. (4) No page states the pgvector or CREATE EXTENSION requirement. (5) The compose.yaml healthcheck comment says "`-config` and `-version` are its only flags"; the binary also has -role and -worker-id.

Impact: Operators follow wrong restore and upgrade steps and misjudge the security posture.

Suggested fix: Correct these pages, add a "PostgreSQL requirements" section, and add a docs lint that flags references to closed tickets in operator docs.

Files: docs/src/content/docs/operate/upgrades.md, docs/src/content/docs/start/install.md, docs/src/content/docs/operate/security.md, docs/src/content/docs/operate/deployment.md

Existing tickets: FEAT-a94c8y, FEAT-m94hhx

---
### OpenAPI declares no security schemes, and the API reference generator hard-codes "no operation requires authentication" [find:build-health-dx] (medium/docs) · area: OpenAPI / API reference · confidence: high

With auth enabled, every operation requires a session cookie or a Bearer API key, but the spec has no securitySchemes and no security requirements. The docs generator writes a fixed sentence saying no operation needs auth, so regenerating the reference will not fix it.

Evidence: /api/openapi.json (63 ops): no components.securitySchemes and no global or per-operation security. internal/api/middleware/auth.go:166-172 reads `Authorization: Bearer`. scripts/generate-api-reference.mjs:217 hard-codes the sentence, which lands in docs/src/content/docs/reference/api.md:34. unfinished-work separately reports the stale operation counts in that page.

Impact: /docs (Scalar) offers no auth input, generated clients carry no auth typing, and readers conclude the API is open by design.

Suggested fix: Register bearer (API key) and cookie security schemes in the Huma config, exempting public ops (health, ready, login, webhook, resume). Rewrite the generator sentence to explain auth-off-by-default versus auth-on.

Files: internal/api/server.go, scripts/generate-api-reference.mjs, docs/src/content/docs/reference/api.md

---
### Embed signing key is documented as "at least 32 bytes", but boot requires exactly 32 decoded bytes [find:build-health-dx] (low/docs) · area: config docs · confidence: high

The config struct comment (and the generated example and reference built from it) says at least 32 bytes. decodeKey accepts only exactly 32 bytes (base64, hex or raw), so longer keys are refused at boot.

Evidence: internal/config/config.go:400-401 -> config.example.yaml:225 and docs/src/content/docs/operate/configuration-reference.md:498. guides/embedding.md:353 says "exactly 32 bytes". internal/credentials/keysource.go:69-81. Live: a 48-byte key (openssl rand -base64 48) exits with `embed signing key: KILASFLOW_EMBED_SIGNING_KEY must hold a 32-byte key encoded as base64, hex, or raw bytes`. auth.signing_key_env has no length doc at all (config.go:304).

Impact: First-time embed setup fails at boot for operators who follow the reference and generate a longer key.

Suggested fix: Change the comment to "exactly 32 bytes (openssl rand -base64 32)", document the same for auth.signing_key_env, and regenerate the reference.

Files: internal/config/config.go, config.example.yaml, docs/src/content/docs/operate/configuration-reference.md

---
### The committed web API client is stale vs the server spec (datastore-scoped embed sessions missing); the CI drift job hides the other stale artifacts [find:build-health-dx] (low/dx) · area: web generated client / CI drift job · confidence: high

The web client's EmbedSessionBody still requires workflowId and lacks datastoreId, while the server (and the SDK types) support datastore-scoped sessions. The CI drift job stops at this first failure, so the SDK-types, docs-reference and config-reference checks after it are skipped.

Evidence: I ran web's orval against the running binary's /api/openapi.json into scratch and diffed the models: web/src/lib/api/generated/models/embedSessionBody.ts lacks `datastoreId?` and has `workflowId: string` (required); embedSessionResource.ts lacks datastoreId. sdk/src/generated/models.ts:501-521 already has both. Introduced by merge cd8d148 (FEAT-1c70nt). CI run 34571965928: step "Web API client" failed with "Generated API client is stale", and the next three drift steps were skipped.

Impact: The web types misdescribe the API and CI stays red. Other stale generated artifacts (for example the docs API reference) are not surfaced.

Suggested fix: Run make generate-api and commit. Make each drift step independent (if: always()) so all stale outputs are reported at once.

Files: web/src/lib/api/generated/models/embedSessionBody.ts, .github/workflows/ci.yml

Existing tickets: FEAT-1c70nt

---
### CLI rough edges: stray arguments start a full server, there is no probe command for the distroless image, and fatal errors bypass the JSON logger [find:build-health-dx] (low/dx) · area: cmd/kilasflow · confidence: high

flag.Parse is never followed by a check for positional args. There is no healthcheck/version/migrate subcommand. Boot failures are printed as plain text even in JSON log mode.

Evidence: cmd/kilasflow/main.go:57-64. During this audit, `kilasflow version` silently booted a server on 0.0.0.0:8080 using ./data/kilasflow.db in the CWD, and it had to be stopped. `kilasflow -h` prints `-role ... (default both) (default "both")`. compose.yaml admits no healthcheck is possible (no shell or curl in the image, no probe subcommand), so `depends_on: service_healthy` cannot target KilasFlow. With KILASFLOW_LOG_FORMAT=json (the Docker default), a boot failure appears as `kilasflow: embed signing key: ...` in plain text, which is exactly the line log shippers need during a crash loop.

Impact: Accidental servers and DB files, no container health gating, and harder crash diagnosis.

Suggested fix: Reject unexpected positional args, or add `version`, `healthcheck` (GET /api/v1/ready on the configured port, usable as an exec-form HEALTHCHECK) and `migrate` subcommands. Drop the duplicated default from the usage text. Log fatal errors through slog before exit.

Files: cmd/kilasflow/main.go, compose.yaml, Dockerfile

---
### Vite dev proxy omits /resume, so the approval page cannot resume executions under `make dev` [find:build-health-dx] (low/dx) · area: web dev server · confidence: high

The documented dev flow (open :5173) proxies only /api, /webhook and /docs. The approval page POSTs to /resume/<token>, which Go serves but Vite does not forward.

Evidence: web/vite.config.ts:17 proxied = ['/api','/webhook','/docs'] with the comment "Keep these prefixes in step with internal/api/server.go". internal/api/routes.go:61-63 mounts /resume (internal/engine/wait_service.go:37), which is missing from the server.go prefix constants. web/src/routes/approve/[token]/+page.svelte:39,63 fetch(`/resume/${token}`). scripts/smoke-dev.sh does not cover it.

Impact: Developers cannot exercise human-approval and Wait resume flows in dev mode.

Suggested fix: Add '/resume' to the proxy list and ResumePrefix to server.go's prefix block. Have smoke-dev probe every mounted prefix.

Files: web/vite.config.ts, internal/api/server.go, internal/api/routes.go

---
### The devbox shell exports CGO_ENABLED=0, so `devbox run test` / `make test` (go test -race) fails on Linux [find:build-health-dx] (low/dx) · area: devbox / Makefile · confidence: high

The devbox init hook disables cgo for the whole shell, but the Makefile's test target uses -race, which requires cgo on Linux. The hook also exports an unused KILASFLOW_ENV.

Evidence: devbox.json init_hook `export CGO_ENABLED=0`; scripts.test = "make test"; Makefile test = `go test ./... -race`. `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -race -c ./internal/config` -> "go: -race requires cgo" (it works on darwin because the macOS race runtime no longer needs cgo). KILASFLOW_ENV=development is referenced nowhere, and the config loader silently swallows it.

Impact: Linux contributors using devbox cannot run the test suite as documented.

Suggested fix: Keep CGO_ENABLED=0 only in the build recipe (it is already set there), drop it from the shell hook, and remove KILASFLOW_ENV.

Files: devbox.json, Makefile

## Acceptance criteria

- [ ] build-web overwrites the tracked SPA placeholder: builds report "-dirty", and the built index.html has already
- [ ] Postgres-gated Go tests share one database across packages and interfere with each other under parallel `go te
- [ ] 27% of the Playwright suite never runs in CI (26/97 skipped), including every PostgreSQL, WAHA-migration, loca
- [ ] The embedded SPA is served without compression or cache headers, and missing hashed assets fall back to index.
- [ ] Operator docs contradict the shipped product: PostgreSQL image, migration count, internal-DB guard status, CLI
- [ ] OpenAPI declares no security schemes, and the API reference generator hard-codes "no operation requires authen
- [ ] Embed signing key is documented as "at least 32 bytes", but boot requires exactly 32 decoded bytes
- [ ] The committed web API client is stale vs the server spec (datastore-scoped embed sessions missing); the CI dri
- [ ] CLI rough edges: stray arguments start a full server, there is no probe command for the distroless image, and 
- [ ] Vite dev proxy omits /resume, so the approval page cannot resume executions under `make dev`
- [ ] The devbox shell exports CGO_ENABLED=0, so `devbox run test` / `make test` (go test -race) fails on Linux
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress 2026-09-19 (SecurityDx)
- Status: doing. Research + partial implementation done this session.

## Progress 2026-09-20 (DXOps2)
- SPA placeholder / dirty build: dist/index.html is no longer tracked (git rm --cached); the placeholder is
  internal/web/placeholder/index.html, embedded separately and served when dist carries no index.html, so a
  fresh clone + `go build` explains itself instead of 404ing or serving a blank editor. `make build-web` now
  only writes gitignored output, so `git describe --dirty` stays clean. Makefile: dist-placeholder target
  deleted, build-clean-check added.

- CLI rough edges: `kilasflow version` (and any stray positional argument) now exits 1 with a usage error
  instead of booting a server against ./data; -h no longer shows the duplicated "(default both)"; fatal errors
  go through slog when KILASFLOW_LOG_FORMAT=json.
- Embed/auth signing key comments now say exactly 32 bytes and name `openssl rand -base64 32`;
  config.example.yaml + operate/configuration-reference.md regenerated (generate-config-reference-check passes).
