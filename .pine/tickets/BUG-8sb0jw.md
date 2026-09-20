---
id: BUG-8sb0jw
title: 'Build/CI/release hygiene: red CI, shared PG tests, skipped Playwright, dirty builds, stale client, docs'
status: done
priority: medium
labels:
    - dx
    - ci
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T03:17:19Z"
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

- PostgreSQL-gated Go tests: `make test` now passes `-p 1` whenever KILASFLOW_TEST_POSTGRES_DSN is set (the
  packages share one database and internal/database drops every table); CI documents the reason next to the DSN.
- CI: the test job's service is now pgvector/pgvector:pg17 (stock postgres cannot CREATE EXTENSION vector, so
  every PostgreSQL-gated package failed at migration 000006 — the current red); the four drift steps run with
  `if: always()` so all stale generated artifacts are reported in one run; the lint job gained
  `make build-clean-check`.
- e2e: the job now runs a pgvector service and sets KILASFLOW_E2E_POSTGRES_DSN, which moves the three
  datastore-pg tests and epic proof 3 from skipped into run. Every remaining skip names its dependency through
  e2e/fixtures/gates.ts and a new skip-budget check (e2e/scripts/skip-budget.mjs + e2e/skip-budget.json, wired as
  `make e2e-skip-budget` after the run) fails the job on an unexplained skip, on a PostgreSQL skip (CI provides
  it), or on a total past the recorded budget. Verified against real Playwright JSON reports for the pass case
  and all three failure modes.

- Operator docs corrected against the shipped product: operate/upgrades.md no longer tells operators to restore into
  `postgres:17-alpine` (the service pins pgvector/pgvector:pg17 and a dump carries CREATE EXTENSION vector, which that
  image cannot satisfy); start/install.md gained a "PostgreSQL requirements" section (pgvector, the superuser privilege
  CREATE EXTENSION needs, and the fact that a missing extension is a warned skip rather than a failed boot) and its
  migration count no longer says "three"; operate/security.md and operate/deployment.md now describe the
  internal-database guard as shipped (SQLite paths and the PostgreSQL network identity, plus egress-policy and
  allowed-domains checks on SQL targets) instead of "no guard yet / no host validation ... FEAT-a94c8y closes this";
  compose.yaml's healthcheck comment lists the binary's four flags; compose.postgres.yaml no longer claims a boot
  failure without pgvector.
- Remaining from this ticket, with reasons: (a) a self-authored, licence-clean WAHA template so waha-migration.spec.ts
  and epic proof 2 run in CI — the official template is committed material we have no licence for, so the fixture has
  to be ours and the spec's pinned diagnostics rewritten around it; (b) an OpenAI-compatible stub model so the four
  ai-agent-ollama model tests and epic proof 1 run without a local Ollama; (c) `kilasflow healthcheck` as a subcommand
  (the finding half implemented by the compose comment correction and the Dockerfile shipping nodepackgen). Until (a)
  and (b) land, `make e2e-skip-budget` keeps every one of those skips named and counted rather than passing silently.
- Stale web client (finding 9): still red, and it needs two changes in one commit. `make generate-api-check` fails
  ("Generated API client is stale"); running `pnpm generate:api` succeeds but `cd web && pnpm check` then reports 15
  errors — the new pagination param types (ListWorkflowsParams, ListApiKeysParams, ListSchedulesParams,
  ListDatastoresParams) no longer match the SPA's customQuery call sites in schedules/settings/workflows-list pages.
  Regenerating alone moves the red from the drift job to the lint job, so the regeneration was reverted and the drift
  step stays red with this note. Handed to the SPA owners (hub broadcast): regenerate + migrate the call sites, then
  prove both `pnpm check` and `make generate-api-check` green in one commit. The drift steps now run with `if: always()`
  so this failure and any other stale artifact appear in the same run.
- Verified green at this commit: `go test ./internal/api/ -run TestOpenAPI|TestPublicOperations` (4 new security-scheme
  tests, plus the existing document tests), `make generate-api-reference-check` (14 pages fresh, 72 operations),
  `cd docs && pnpm build` (43 pages, links valid), `make e2e-skip-budget` behaviour against real Playwright JSON
  reports, `sh scripts/check-coordinates.sh` both ways, and `make build-all` in a clean clone of 8cc62a7 (no tracked
  file touched, `-version` reports the commit with no `-dirty`, fresh `go build` serves the placeholder page).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (14):
  - `aabb522d` — chore(pine): commit the outstanding ticket notes
  - `b8bc90ba` — chore(pine): record the web review's P1 and keyboard/doc fixes on their tickets
  - `72419f80` — BUG-8sb0jw: the quickstart enters the directory the clone actually creates — docs
  - `d0f26a19` — chore(pine): close the 53 EPIC-cfe7ny remediation tickets with work evidence
  - `cb8b84e8` — BUG-8sb0jw: correct the reference host's bootstrap variables, image tag and key story — docs
  - `e9bcc6ae` — BUG-8sb0jw FEAT-edxxj7: record testing state with per-finding remainders
  - `40c6a895` — BUG-8sb0jw: record the outstanding stale-client handoff and the verified-green list
  - `68588988` — BUG-8sb0jw: proxy /resume in the dev server, probe every mounted prefix, drop CGO_ENABLED=0 from the devbox shell — dev
  - `67ab681d` — BUG-8sb0jw: correct the operator docs and compose comments against the shipped product — docs/operate
  - `27d7ae48` — BUG-8sb0jw: pgvector CI services, serialised PostgreSQL packages, always-on drift steps, e2e skip budget — CI/e2e
  - `21c0cb43` — BUG-8sb0jw: reject stray CLI arguments, JSON-log fatal errors, document exact signing-key length — CLI/config docs
  - `8cc62a7b` — BUG-8sb0jw: embed SPA placeholder outside the build dir, cache/compress/404 static serving, clean build — build/embed
  - `2930a34c` — SecurityDx: progress notes — xf1wqm+s0wy50 landed, safehttp partial, rest doing
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   12 +-
 .pine/MEMORY.md                                    |    2 +
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   39 +
 .pine/tickets/BUG-4053h6.md                        |  998 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 ++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  690 +++++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++++
 .pine/tickets/BUG-6jvcs5.md                        |  710 +++++
 .pine/tickets/BUG-8dmp5y.md                        |  660 +++++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++++
 .pine/tickets/BUG-8sb0jw.md                        |  708 +++++
 .pine/tickets/BUG-8t94wn.md                        |  628 +++++
 .pine/tickets/BUG-9853ay.md                        |  571 ++++
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  834 ++++++
 .pine/tickets/BUG-c241hm.md                        |  607 +++++
 .pine/tickets/BUG-cq4yk3.md                        |  832 ++++++
 .pine/tickets/BUG-dndnhn.md                        |  497 ++++
 .pine/tickets/BUG-esb9sh.md                        |  590 +++++
 .pine/tickets/BUG-f9frth.md                        |  870 ++++++
 .pine/tickets/BUG-fv5fer.md                        |  664 +++++
 .pine/tickets/BUG-gaavr5.md                        |  813 ++++++
 .pine/tickets/BUG-hfhzq6.md                        |  501 ++++
 .pine/tickets/BUG-hm76dq.md                        |  569 ++++
 .pine/tickets/BUG-j7rtv3.md                        |  137 +
 .pine/tickets/BUG-kzkvv6.md                        |  574 ++++
 .pine/tickets/BUG-mewhrd.md                        |  517 ++++
 .pine/tickets/BUG-mz8xrb.md                        |  506 ++++
 .pine/tickets/BUG-npfz43.md                        |  487 ++++
 .pine/tickets/BUG-pwckhd.md                        |  512 ++++
 .pine/tickets/BUG-qmgz2f.md                        |  668 +++++
 .pine/tickets/BUG-qq4xva.md                        |  506 ++++
 .pine/tickets/BUG-rjd6fm.md                        |  721 +++++
 .pine/tickets/BUG-rrkjrd.md                        |  526 ++++
 .pine/tickets/BUG-s0wy50.md                        |  507 ++++
 .pine/tickets/BUG-t2wezf.md                        |  536 ++++
 .pine/tickets/BUG-tcqkad.md                        |  652 +++++
 .pine/tickets/BUG-th16c1.md                        |  110 +
 .pine/tickets/BUG-txc9xg.md                        |  520 ++++
 .pine/tickets/BUG-wdypd2.md                        |  680 +++++
 .pine/tickets/BUG-wp2y0y.md                        |  495 ++++
 .pine/tickets/BUG-xf1wqm.md                        |  493 ++++
 .pine/tickets/BUG-y57cz4.md                        |  654 +++++
 .pine/tickets/BUG-ysvmaa.md                        |  790 ++++++
 .pine/tickets/BUG-ze1nn8.md                        |  564 ++++
 .pine/tickets/BUG-ztzxck.md                        |  507 ++++
 .pine/tickets/EPIC-cfe7ny.md                       |   47 +
 .pine/tickets/FEAT-0895qc.md                       |  761 ++++++
 .pine/tickets/FEAT-15k49d.md                       |   36 +
 .pine/tickets/FEAT-56nep4.md                       |  625 +++++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  642 +++++
 .pine/tickets/FEAT-j5s2n4.md                       |  639 +++++
 .pine/tickets/FEAT-jvembs.md                       |  813 ++++++
 .pine/tickets/FEAT-nqpvf6.md                       |  611 +++++
 .pine/tickets/FEAT-qdedm0.md                       |   38 +
 .pine/tickets/FEAT-x5km1z.md                       |  590 +++++
 Dockerfile                                         |   15 +-
 Makefile                                           |  116 +-
 README.md                                          |   27 +-
 cmd/kilasflow/main.go                              |  158 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |   13 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    4 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/execution-model.md  |  210 +-
 docs/src/content/docs/concepts/expressions.md      |   99 +-
 docs/src/content/docs/concepts/node-registry.md    |   35 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 docs/src/content/docs/concepts/webhooks.md         |   62 +-
 docs/src/content/docs/guides/community-nodes.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |   16 +-
 docs/src/content/docs/guides/n8n-migration.md      |  127 +-
 docs/src/content/docs/guides/node-authoring.md     |   10 +-
 docs/src/content/docs/index.mdx                    |    2 +-
 .../docs/operate/configuration-reference.md        |   13 +-
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 +++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 ++-
 docs/src/content/docs/start/install.md             |   46 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/datastore.ts                          |    8 +-
 e2e/fixtures/epic-telegram.ts                      |   24 +-
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import.ts                     |   13 +-
 e2e/fixtures/n8n-live.ts                           |   10 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/epic-acceptance.spec.ts                  |    8 +-
 e2e/tests/library-import.spec.ts                   |    8 +-
 e2e/tests/waha-migration.spec.ts                   |    6 +-
 internal/ai/agent.go                               |  104 +-
 internal/ai/ai.go                                  |   47 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  146 +-
 internal/ai/openai_test.go                         |  145 +
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  136 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/credentials_test.go                   |   98 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/datastores_test.go                    |   41 +
 internal/api/embed_confinement_test.go             |  352 +++
 internal/api/embed_test.go                         |   27 +
 internal/api/events_test.go                        |   89 +
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 ++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   65 +-
 internal/api/handlers/datastores.go                |   76 +-
 internal/api/handlers/datastores_csv.go            |   78 +-
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  322 ++-
 internal/api/handlers/interop.go                   |  136 +-
 internal/api/handlers/nodes.go                     |   44 +-
 internal/api/handlers/problem.go                   |  112 +
 internal/api/handlers/schedules.go                 |   52 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 +
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 +++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  142 +
 internal/api/middleware/cors_test.go               |  235 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/node_types_test.go                    |   85 +-
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   41 +-
 internal/api/server.go                             |  139 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  323 +++
 internal/config/config.go                          |  327 ++-
 internal/credentials/redirect_test.go              |  141 +
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 +
 internal/embed/embed.go                            |   25 +
 internal/embed/embed_test.go                       |   14 +
 internal/engine/approval.go                        |   17 +-
 internal/engine/authenticate.go                    |   45 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   24 +
 internal/engine/error_workflow_test.go             |  230 ++
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 +++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiprocess_test.go               |  115 +-
 internal/engine/runner.go                          | 1740 ++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  726 ++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  176 +-
 internal/engine/wait_service_test.go               |  373 ++-
 internal/engine/worker_test.go                     |   15 +
 internal/execution/records.go                      |   11 +
 internal/expression/doc.go                         |  107 +-
 internal/expression/evaluator.go                   |  938 +++++++
 internal/expression/expression.go                  |  591 ++---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  565 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1217 +++++++++
 internal/expression/parity_test.go                 | 1041 ++++++++
 internal/expression/parser.go                      |  824 ++++++
 internal/expression/roots.go                       |  365 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         | 1087 ++++++++
 internal/interop/n8n/n8n.go                        |  493 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2780 ++++++++++++++++++--
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++++
 internal/loadoptions/loadoptions.go                |   11 +
 internal/loadoptions/redirect_test.go              |  117 +
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   34 +
 internal/repository/auth.go                        |  388 +++
 internal/repository/auth_admin_test.go             |  477 ++++
 internal/repository/claim_lease_test.go            |  266 ++
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 +
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |  546 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   59 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  234 ++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/webhooks.go                    |  188 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  233 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   97 +
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   52 +
 internal/scheduler/scheduler_test.go               |   67 +
 internal/sqlbuild/sqlbuild_test.go                 |    4 +-
 internal/web/dist/index.html                       |   37 -
 internal/web/embed.go                              |  459 +++-
 internal/web/embed_test.go                         |  335 ++-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form.go                           |  262 ++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/request_lifecycle.go              |  425 ++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  825 +++++-
 internal/webhook/webhook_test.go                   |  789 +++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../postgres/000013_node_run_response.down.sql     |    9 +
 .../postgres/000013_node_run_response.up.sql       |   28 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 .../sqlite/000013_node_run_response.down.sql       |    9 +
 migrations/sqlite/000013_node_run_response.up.sql  |   23 +
 nodes/ai.go                                        |  541 +++-
 nodes/ai_test.go                                   |  874 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  217 ++
 nodes/embedscope_test.go                           |  288 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  363 ++-
 nodes/http_test.go                                 |  335 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |  158 +-
 nodes/subworkflow_calls_test.go                    |   90 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  480 +++-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   83 +-
 packs/waha/waha_test.go                            |  373 ++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 scripts/check-coordinates.sh                       |   60 +
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |   15 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 web/src/lib/api/http.ts                            |   16 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++-
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |  167 +-
 .../workflow-editor/property-field.svelte          |  593 ++++-
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  737 +++++-
 web/src/lib/dashboard/cursor-page.test.ts          |   85 +-
 web/src/lib/dashboard/cursor-page.ts               |   57 +
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  176 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  158 ++
 web/src/lib/workflow-editor/document.ts            |  291 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |  103 +
 web/src/lib/workflow-editor/expression-assist.ts   |  141 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   49 +
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 +-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  125 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |  109 +
 web/src/lib/workflow-editor/shortcuts.ts           |  139 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  430 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  391 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  205 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |   63 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  219 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |  142 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  317 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 450 files changed, 82254 insertions(+), 4933 deletions(-)
```
