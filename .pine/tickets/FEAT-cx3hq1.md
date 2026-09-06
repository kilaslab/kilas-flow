---
id: FEAT-cx3hq1
title: Stand up the browser end-to-end harness
status: done
priority: high
labels:
    - e2e
    - testing
deps:
    - FEAT-7tgasa
parent: EPIC-m42s3g
phase: p11
created: "2026-09-05T12:01:57Z"
updated: "2026-09-06T03:37:32Z"
---

## Scope

There is no browser test infrastructure of any kind. `web/package.json` declares `vitest` and nothing else — no Playwright, no Cypress, no `@playwright/test` — and there is no `e2e` directory anywhere in the tree. The only artefacts resembling browser evidence are stray console logs and screenshots under `.playwright-mcp/`, produced by an interactive session rather than by a suite.

Everything the project verifies today stops at a boundary the user never sees. Go tests cover the engine, the compiler, the importer and the handlers. The four smoke scripts boot a real binary and assert `/api/v1/health`, `/api/openapi.json` and that the SPA fallback returns `<!doctype html>` — which proves the page is served, not that it works. `vitest` covers isolated frontend units. Between "the server answers" and "a person can build a workflow and watch it run" there is no coverage at all.

That gap is where this product's whole value sits. The editor is a Svelte Flow canvas with a node picker, a parameter panel driven by server-supplied property metadata, an expression editor, a credential picker, an execution event stream over SSE, and an embedded mode with a postMessage handshake. Every one of those is a place where a server change lands silently: `node.Definition` is serialized directly as the `/api/v1/node-types` payload, so any p2 metadata change reshapes the editor's forms without touching editor code.

This ticket builds the harness only. The suites that use it are V2-p11-3 through V2-p11-7, and each is separately scoped so this one does not become a bottleneck.

Three properties the harness must have, because they decide whether the suites are worth trusting.

**A real binary, a real database, a real SPA.** The value of an end-to-end test here is that it exercises the same composition path `cmd/kilasflow/main.go` builds — registry, executors, routing, webhook bindings, lifecycle verification. A harness that mocks the API tests the editor against a fiction of the server, which is precisely the drift the repository already spends effort preventing.

**Isolation per test.** `execution.max_concurrent` defaults to 10 and the scheduler runs; tests that share a database will interfere in ways that read as flakiness. Each test needs its own data directory, its own port and its own workflows.

**Determinism without mocking the product.** Real outbound calls to third-party APIs make a suite that fails on somebody else's outage. The seam already exists and is the right one: `internal/safehttp` governs every outbound request, so a local stub server plus an `allowed_hosts` entry gives determinism without weakening anything the product does.

## Acceptance criteria

- [ ] A Playwright project exists with a fixture that builds the binary and the SPA once, then boots a fresh instance per test file with its own port and data directory.
- [ ] A test can seed a tenant, a workflow, a credential and a schedule without driving the UI, so a test about the editor is not also a test about the create form.
- [ ] Outbound HTTP from a workflow under test reaches a local stub rather than the internet, configured through the existing `outbound.allowed_hosts` policy rather than by disabling the guard.
- [ ] The harness waits on observable application state — an execution status, a rendered node, an SSE event — and contains no fixed sleeps.
- [ ] A failing test emits a trace, a screenshot and the server's log for that instance, and the artefacts identify which instance produced them.
- [ ] Running the suite twice in a row from a dirty working tree produces the same result, and running it in parallel does not produce cross-test interference.
- [ ] `make test-e2e` runs the suite the same way locally and in the V2-p10-1 pipeline.
- [ ] The harness covers the embedded editor as well as the dashboard, since the embed frame is a separate route with its own postMessage handshake.

## Implementation Plan

Put the project in `e2e/` at the repository root rather than inside `web/`. It tests the assembled product — Go binary plus embedded SPA — not the frontend package, and `web/`'s `vitest` suite should stay a unit suite.

Build once, boot many. `scripts/openapi-spec.mjs` already demonstrates the exact pattern this needs: build a binary into a temp directory, reserve a free port by opening and closing a listener, spawn with `-config ''`, poll `/api/v1/ready`, tear down with SIGTERM then SIGKILL. Reuse that logic rather than reinventing it; the port-reservation detail in particular is what stops parallel workers colliding.

Note the constraint that a Go build is now a prerequisite of the frontend test suite, and that the SPA must be built into `internal/web/dist` before the binary is compiled or the embedded app will be the placeholder page. `make build-all` does both in the right order; a harness that runs `go build` alone will serve "SPA not built" and produce failures that look like editor bugs.

For determinism, prefer a stub over interception. Playwright's request interception operates in the browser, and the requests that matter here — a workflow's HTTP node calling an API — are made by the Go process, where the browser cannot see them. The stub must be a real HTTP server the workflow reaches, allowed through `outbound.allowed_hosts`. Do not set `allow_private_networks: true` globally to make this easier; that turns off the guard the product exists to have, and a suite running with a different security posture than production is a suite that cannot catch a security regression.

Seeding should go through the API rather than the database. It is the supported surface, it is what a host application would do, and a seeder writing GORM rows directly would need updating every time p6's migrations change the schema.

One decision to settle rather than default: whether the suite runs against a built container image or a locally built binary. Recommend the binary for the developer loop and the image in the pipeline once V2-p10-2 publishes one — the image is what ships, and `smoke-docker` already proves the pattern of testing a container.

## References

- Roadmap plan, p11 section, entry V2-p11-1: `.pine/roadmap.md`.
- `scripts/openapi-spec.mjs` — the build, free-port reservation, readiness poll and teardown pattern to reuse.
- `scripts/smoke-sqlite.sh`, `scripts/smoke-docker.sh` — the existing end-to-end assertions this suite supersedes and extends.
- `Makefile` — `build-all`, `dist-placeholder`, and where `test-e2e` belongs.
- `internal/web/embed.go` — why the SPA must be built before the binary.
- `internal/config/config.go` — `OutboundHTTP.AllowedHosts` and `AllowPrivateNetworks`, the determinism seam.
- `internal/safehttp/safehttp.go` — the dial-time address check the stub must be allowed through rather than around.
- `cmd/kilasflow/main.go` — the composition an end-to-end test is meant to exercise.
- `web/src/routes/embed/[id]/+page.svelte`, `sdk/src/browser.ts` — the embedded surface and its handshake.
- `.pine/tickets/FEAT-7tgasa.md` — V2-p10-1, the pipeline this suite runs in.

## Outcome

Done 2026-09-06. Harness + one smoke proof, all against a real binary, a real
SQLite database and the real embedded SPA.

New: `e2e/` (Playwright project at the root, not in `web/`), `scripts/e2e-stub.mjs`,
`test-e2e` in the Makefile, an `e2e` job in `.github/workflows/ci.yml`.

- `e2e/playwright.config.ts` — chromium, `globalSetup` builds once via
  `make build-all` (SPA into `internal/web/dist` before `go build`, or the
  binary serves the placeholder), `trace`/`screenshot`/`video` on failure,
  `fullyParallel`.
- `e2e/helpers/server.ts` — per-test instance: own port (open-close listener
  reservation, the `scripts/openapi-spec.mjs` pattern), own data dir,
  `-config ''`, `/api/v1/ready` poll, SIGTERM-then-SIGKILL. Auth stays off
  (default tenant, no login); embed enabled with a random per-instance key;
  `KILASFLOW_ENCRYPTION_KEY` set (credential seeding 422s without it).
- Outbound seam is `outbound.allowed_hosts=127.0.0.1` plus one
  `outbound.allowed_private_endpoints=127.0.0.1:<stub-port>` entry. A second
  loopback (`127.0.0.2`, same port) is refused at the guard — proven by a
  failed execution, not by absence of a call. `allow_private_networks` is
  never set. Single-entry values only: the koanf env mapping carries one
  value per variable (proven by `config_test.go`'s own single-entry case).
- `e2e/helpers/seed.ts` — tenant/workflow/credential/schedule/run/embed-session
  all through the REST API, never GORM rows. (No tenant endpoint exists:
  `tenants.go` is a resolver; the binary bootstraps the default tenant.)
  `waitForExecution` polls durable status via `expect.poll`; `readExecutionEvents`
  reads the SSE feed to its terminal event. No fixed sleeps anywhere.
- `e2e/helpers/stub.ts` — real loopback HTTP server (Playwright interception
  cannot see Go-process requests) that also serves the embed host page, keeping
  the host cross-origin from the instance like production.
- `e2e/fixtures.ts` — fresh server+stub per test (stronger than per-file),
  server log copied to `test-results` and attached on failure, keyed by port.
- `e2e/tests/smoke.spec.ts` — five tests: dashboard lists a seeded workflow
  (plus `[data-dashboard-shell]` proves the real SPA), stub reached + SSE
  `execution.completed`, different loopback refused, schedule listed back,
  embed `kilasflow:embed-ready` handshake completed in a host frame.
- Verified: `npx playwright test --list` enumerates 5; `make test-e2e` green
  twice in a row (5 passed); `playwright-cli` drove a live instance as an
  independent check (dashboard row link, embed direct-visit denial).
- Deps: `@playwright/test@1.63.0` + `@types/node` in `e2e/` only (MIT,
  test-only, never shipped; no n8n contact of any kind per memory/licensing.md).
- Parent-mandated refs used as needed: handshake/IA derived from first-party
  `web/src/lib/embed/session.svelte.ts`, `sdk/src/browser.ts` and the dashboard
  routes; no third-party bytes read or committed. Binary-vs-image decision
  taken as ticket recommends: binary for the loop and the pipeline.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-06.

- Base: `ddfc94c3` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `c94583a2` — feat(nodes): reach parity on the flow-control node family
- Files changed (base → working tree):

```
 .env.example                                       |  186 +
 .github/actions/js-toolchain/action.yml            |   49 +
 .github/workflows/ci.yml                           |  422 +++
 .github/workflows/release.yml                      |   92 +
 .gitignore                                         |    3 +
 .pine/CHECKPOINT.md                                |  148 +
 .pine/MEMORY.md                                    |    6 +
 .pine/memory/code-node.md                          |   74 +
 .pine/memory/docker.md                             |    9 +
 .pine/memory/live-databases.md                     |   40 +
 .pine/memory/persistence.md                        |   14 +
 .pine/memory/web-editor.md                         |   10 +
 .pine/roadmap.md                                   |  273 +-
 .pine/tickets/BUG-9s3htg.md                        |  170 +
 .pine/tickets/BUG-br7ggc.md                        |  189 +
 .pine/tickets/BUG-v6tdjr.md                        |  231 ++
 .pine/tickets/BUG-xmcm8x.md                        |  152 +
 .pine/tickets/EPIC-m42s3g.md                       |   12 +-
 .pine/tickets/FEAT-0556ck.md                       |  135 +
 .pine/tickets/FEAT-096vs9.md                       |  784 +++-
 .pine/tickets/FEAT-0f87fn.md                       |    2 +-
 .pine/tickets/FEAT-12s0e5.md                       |  494 ++-
 .pine/tickets/FEAT-1500sp.md                       |  168 +-
 .pine/tickets/FEAT-1br8at.md                       |    2 +-
 .pine/tickets/FEAT-1c70nt.md                       |    5 +
 .pine/tickets/FEAT-27km39.md                       |   63 +-
 .pine/tickets/FEAT-2f68r8.md                       |    2 +-
 .pine/tickets/FEAT-2phs15.md                       |  413 ++-
 .pine/tickets/FEAT-347egc.md                       |    3 +-
 .pine/tickets/FEAT-45tfmh.md                       |  390 +-
 .pine/tickets/FEAT-48hreg.md                       |   10 +-
 .pine/tickets/FEAT-4d0bje.md                       |  862 ++++-
 .pine/tickets/FEAT-53fht8.md                       |   80 +-
 .pine/tickets/FEAT-55v09k.md                       |    2 +-
 .pine/tickets/FEAT-5fhj6p.md                       |   69 +
 .pine/tickets/FEAT-5fv8gf.md                       |  189 +-
 .pine/tickets/FEAT-5kfctc.md                       |  162 +-
 .pine/tickets/FEAT-5kv1jq.md                       |    3 +-
 .pine/tickets/FEAT-5mvech.md                       |  280 +-
 .pine/tickets/FEAT-5rvtzc.md                       |    2 +-
 .pine/tickets/FEAT-5s1w0t.md                       |    2 +-
 .pine/tickets/FEAT-5z37xh.md                       |   72 +
 .pine/tickets/FEAT-68zzqs.md                       |  391 +-
 .pine/tickets/FEAT-6vfn3s.md                       |    2 +-
 .pine/tickets/FEAT-7cg0cd.md                       |    3 +-
 .pine/tickets/FEAT-7tgasa.md                       |  211 +-
 .pine/tickets/FEAT-8qyfh1.md                       |  455 ++-
 .pine/tickets/FEAT-8r9n21.md                       |    2 +-
 .pine/tickets/FEAT-91as16.md                       |    3 +-
 .pine/tickets/FEAT-9555xz.md                       |    3 +-
 .pine/tickets/FEAT-96p7m3.md                       |  784 +++-
 .pine/tickets/FEAT-9dqn7d.md                       |  374 +-
 .pine/tickets/FEAT-9knk67.md                       |    2 +-
 .pine/tickets/FEAT-a6yg3n.md                       |    2 +-
 .pine/tickets/FEAT-a7p1b2.md                       |  136 +
 .pine/tickets/FEAT-a94c8y.md                       |    2 +-
 .pine/tickets/FEAT-adzn0a.md                       |    2 +-
 .pine/tickets/FEAT-afs850.md                       |    3 +-
 .pine/tickets/FEAT-ajw7wt.md                       |  122 +-
 .pine/tickets/FEAT-az620p.md                       |  450 ++-
 .pine/tickets/FEAT-bp0ytb.md                       |    2 +-
 .pine/tickets/FEAT-bscygc.md                       |  655 +++-
 .pine/tickets/FEAT-c2a081.md                       |    2 +-
 .pine/tickets/FEAT-cgm1y3.md                       |  786 +++-
 .pine/tickets/FEAT-cpdp8y.md                       |   70 +
 .pine/tickets/FEAT-csqgg5.md                       |    5 +-
 .pine/tickets/FEAT-cwz4ac.md                       |   66 +
 .pine/tickets/FEAT-cx3hq1.md                       |  118 +
 .pine/tickets/FEAT-ddzk2k.md                       |   75 +-
 .pine/tickets/FEAT-de8d4c.md                       |   71 +
 .pine/tickets/FEAT-ed6wdy.md                       |   66 +
 .pine/tickets/FEAT-ej0468.md                       |    3 +-
 .pine/tickets/FEAT-fw0m2q.md                       |    2 +-
 .pine/tickets/FEAT-g6wrxm.md                       |  152 +-
 .pine/tickets/FEAT-gg85se.md                       |   69 +
 .pine/tickets/FEAT-gjzgkd.md                       |    2 +-
 .pine/tickets/FEAT-gvn62x.md                       |  146 +-
 .pine/tickets/FEAT-hv4q8e.md                       |    2 +-
 .pine/tickets/FEAT-je4f4t.md                       |    3 +-
 .pine/tickets/FEAT-jwhdsy.md                       |  414 ++-
 .pine/tickets/FEAT-k3fmj1.md                       |    3 +-
 .pine/tickets/FEAT-k3grr5.md                       |    2 +-
 .pine/tickets/FEAT-k65hqv.md                       |    2 +-
 .pine/tickets/FEAT-knpfqf.md                       |    2 +-
 .pine/tickets/FEAT-kwxxd0.md                       |  141 +
 .pine/tickets/FEAT-m94hhx.md                       |  209 +-
 .pine/tickets/FEAT-mvegj5.md                       |   76 +-
 .pine/tickets/FEAT-n5fdz3.md                       |  409 ++-
 .pine/tickets/FEAT-nbqye0.md                       |    2 +-
 .pine/tickets/FEAT-nrfz6m.md                       |  151 +-
 .pine/tickets/FEAT-nxxbs5.md                       |  140 +-
 .pine/tickets/FEAT-pd3p6x.md                       |    2 +-
 .pine/tickets/FEAT-ptyh9w.md                       |   65 +-
 .pine/tickets/FEAT-q81bq4.md                       |  447 ++-
 .pine/tickets/FEAT-qcm5ec.md                       |    2 +-
 .pine/tickets/FEAT-qe6wb8.md                       |    2 +-
 .pine/tickets/FEAT-r6xhnp.md                       |  808 ++++-
 .pine/tickets/FEAT-rj17xj.md                       |    2 +-
 .pine/tickets/FEAT-sar60r.md                       |    3 +-
 .pine/tickets/FEAT-sbnejr.md                       |    3 +-
 .pine/tickets/FEAT-sdjdh2.md                       |   65 +-
 .pine/tickets/FEAT-sfy1tq.md                       |   96 +-
 .pine/tickets/FEAT-snxxny.md                       |  361 +-
 .pine/tickets/FEAT-sp8cfm.md                       |    2 +-
 .pine/tickets/FEAT-t5q318.md                       |    2 +-
 .pine/tickets/FEAT-v8k1tc.md                       |    2 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  398 +-
 .pine/tickets/FEAT-w9kqeg.md                       |    2 +-
 .pine/tickets/FEAT-whn5vb.md                       |    2 +-
 .pine/tickets/FEAT-xr7ga9.md                       |   79 +
 .pine/tickets/FEAT-xx6p22.md                       |   93 +-
 .pine/tickets/FEAT-ybm2pd.md                       |   68 +-
 .pine/tickets/FEAT-ykyfbd.md                       |   72 +
 .pine/tickets/FEAT-yyjfjq.md                       |    3 +-
 .pine/tickets/FEAT-zmfsjd.md                       |  146 +
 .pine/tickets/FEAT-znm60y.md                       |    2 +-
 .pine/tickets/FEAT-ztxs5p.md                       |    2 +-
 Dockerfile                                         |   54 +-
 Makefile                                           |  218 +-
 README.md                                          |  243 +-
 cmd/kilasflow/main.go                              |  356 +-
 cmd/kilasflow/main_test.go                         |   54 +-
 compose.build.yaml                                 |   35 +
 compose.postgres.yaml                              |   73 +
 compose.yaml                                       |  103 +
 config.example.yaml                                |  291 +-
 docker-compose.yml                                 |   41 -
 docs/.gitignore                                    |    6 +
 docs/astro.config.mjs                              |  130 +
 docs/package.json                                  |   20 +
 docs/plugins/base-links.mjs                        |   57 +
 docs/pnpm-lock.yaml                                | 3807 ++++++++++++++++++++
 docs/src/components/ThemeProvider.astro            |   59 +
 docs/src/components/ThemeSelect.astro              |   79 +
 docs/src/content.config.ts                         |   11 +
 docs/src/content/docs/404.md                       |   21 +
 docs/src/content/docs/concepts/architecture.md     |  163 +
 docs/src/content/docs/concepts/credentials.md      |  223 ++
 docs/src/content/docs/concepts/execution-model.md  |  335 ++
 docs/src/content/docs/concepts/expressions.md      |  210 ++
 .../src/content/docs/concepts/items-and-lineage.md |  174 +
 docs/src/content/docs/concepts/node-registry.md    |  319 ++
 .../src/content/docs/concepts/safety-boundaries.md |  283 ++
 .../content/docs/concepts/tenancy-and-embedding.md |  305 ++
 docs/src/content/docs/concepts/webhooks.md         |  203 ++
 docs/src/content/docs/contributing.md              |   89 +
 docs/src/content/docs/guides/embedding.md          |   49 +
 docs/src/content/docs/guides/n8n-migration.md      |  674 ++++
 docs/src/content/docs/guides/node-authoring.md     |   41 +
 docs/src/content/docs/index.mdx                    |   59 +
 docs/src/content/docs/operate/configuration.md     |   71 +
 docs/src/content/docs/operate/deployment.md        |  101 +
 docs/src/content/docs/operate/security.md          |   88 +
 docs/src/content/docs/operate/upgrades.md          |   69 +
 docs/src/content/docs/reference/api-contract.md    |  304 ++
 docs/src/content/docs/reference/api.md             |   48 +
 .../content/docs/reference/expression-grammar.md   |  183 +
 docs/src/content/docs/reference/node-packs.md      |   36 +
 docs/src/content/docs/start/first-workflow.md      |   40 +
 docs/src/content/docs/start/install.md             |  271 ++
 docs/src/content/docs/start/what-kilasflow-is.md   |   84 +
 docs/src/styles/kilasflow.css                      |  138 +
 docs/tsconfig.json                                 |    5 +
 executions-narrow.png                              |  Bin 0 -> 37917 bytes
 gflow-prd-v1.md                                    |   32 +
 internal/ai/agent.go                               |   34 +-
 internal/ai/ai.go                                  |   41 +-
 internal/ai/ai_test.go                             |  166 +
 internal/ai/memory.go                              |  164 +-
 internal/ai/openai.go                              |  100 +-
 internal/ai/openai_test.go                         |  156 +
 internal/api/auth_test.go                          |  668 ++++
 internal/api/credentials_test.go                   |  401 +++
 internal/api/embed_test.go                         |   61 +-
 internal/api/handlers/auth.go                      |  380 ++
 internal/api/handlers/credentials.go               |  284 +-
 internal/api/handlers/executions.go                |   40 +-
 internal/api/handlers/nodes.go                     |  288 +-
 internal/api/handlers/tenants.go                   |   46 +
 internal/api/handlers/workflows.go                 |  186 +-
 internal/api/middleware/auth.go                    |  173 +
 internal/api/node_types_test.go                    |  424 +++
 internal/api/routes.go                             |   54 +-
 internal/api/server.go                             |   46 +-
 internal/api/workflow_history_test.go              |  188 +
 internal/api/workflows_test.go                     |  138 +-
 internal/auth/auth.go                              |   73 +
 internal/auth/auth_test.go                         |  311 ++
 internal/auth/keys.go                              |  197 +
 internal/auth/session.go                           |  274 ++
 internal/conditions/conditions.go                  |  542 +++
 internal/conditions/conditions_test.go             |  231 ++
 internal/conditions/doc.go                         |   26 +
 internal/config/config.go                          |  477 ++-
 internal/config/config_test.go                     |  325 ++
 internal/credentials/builtin.go                    |   36 +
 internal/credentials/credentials_test.go           |    2 +
 internal/credentials/registry.go                   |   14 +-
 internal/database/database.go                      |   71 +-
 internal/database/database_test.go                 |   91 +-
 internal/database/migrate.go                       |  648 ++++
 internal/database/migrate_test.go                  |  879 +++++
 internal/datetime/datetime_test.go                 |  134 +
 internal/datetime/doc.go                           |   15 +
 internal/datetime/format.go                        |  195 +
 internal/datetime/parse.go                         |  108 +
 internal/embed/embed.go                            |    2 +
 internal/engine/authenticate.go                    |   13 +-
 internal/engine/runner.go                          |   49 +
 internal/engine/service.go                         |  303 +-
 internal/engine/service_test.go                    |    8 +-
 internal/engine/subworkflow_test.go                |  329 ++
 internal/engine/worker_test.go                     |    5 +
 internal/execution/records.go                      |   14 +-
 internal/expression/doc.go                         |   19 +-
 internal/expression/expression.go                  |   22 +
 internal/expression/expression_test.go             |   65 +
 internal/guardrails/shell_injection_test.go        |   70 +
 internal/interop/n8n/corpus/BASELINE.md            |   19 +-
 internal/interop/n8n/corpus/baseline.json          |   72 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |   43 +-
 internal/interop/n8n/export_test.go                |   15 +
 internal/interop/n8n/n8n.go                        |  430 ++-
 internal/interop/n8n/n8n_test.go                   | 1560 +++++++-
 internal/interop/n8n/parameters.go                 | 2252 +++++++++++-
 internal/interop/n8n/sqlfidelity_test.go           |  442 +++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   93 +
 internal/loadoptions/loadoptions.go                |   61 +-
 internal/loadoptions/loadoptions_test.go           |   99 +
 internal/loadoptions/schema.go                     |   68 +
 internal/loadoptions/sql.go                        |  287 ++
 internal/loadoptions/sql_test.go                   |  350 ++
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/registry.go                          |  109 +-
 internal/node/registry_test.go                     |  247 +-
 internal/nodepack/nodepack.go                      |   16 +-
 internal/property/locator_test.go                  |  116 +
 internal/property/mapper.go                        |  322 ++
 internal/property/mapper_test.go                   |  196 +
 internal/property/property.go                      |  189 +-
 internal/repository/auth.go                        |  330 ++
 internal/repository/auth_test.go                   |  322 ++
 internal/repository/execution_retention.go         |  180 +
 internal/repository/execution_retention_test.go    |  396 ++
 internal/repository/executions.go                  |  206 +-
 internal/repository/models.go                      |  212 +-
 internal/repository/models_test.go                 |   12 +-
 internal/repository/postgres_execution_test.go     |  213 ++
 internal/repository/schedules.go                   |  140 +-
 internal/repository/workflow_history.go            |  446 +++
 internal/repository/workflow_history_test.go       |  613 ++++
 internal/repository/workflows.go                   |  182 +-
 internal/routing/request.go                        |    6 +-
 internal/runcode/doc.go                            |   37 +-
 internal/runcode/runcode.go                        |   96 +-
 internal/runcode/runcode_test.go                   |  184 +-
 internal/safehttp/safehttp.go                      |  139 +-
 internal/safehttp/safehttp_test.go                 |  193 +
 internal/scheduler/extract.go                      |   81 +
 internal/scheduler/item.go                         |   71 +
 internal/scheduler/rule.go                         |  321 ++
 internal/scheduler/rule_test.go                    |  278 ++
 internal/scheduler/scheduler.go                    |  139 +-
 internal/scheduler/scheduler_test.go               |  155 +-
 internal/sqlbuild/dialect.go                       |  256 ++
 internal/sqlbuild/sqlbuild.go                      |  392 ++
 internal/sqlbuild/sqlbuild_test.go                 |  650 ++++
 .../testdata/fuzz/FuzzIdentifier/074b649c870c7bf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/0ef06ad2d24324a6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/10abc61b4419477c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1138f8b07d3718ed  |    2 +
 .../testdata/fuzz/FuzzIdentifier/11956af7e5f573cf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/143ed5c16fb0545a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/18f080b7181cbda6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a1bc12d893c9520  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a4b8093f72994e4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1bb2624596902fcc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1d590be519b71d94  |    2 +
 .../testdata/fuzz/FuzzIdentifier/202cac18931087b8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/25f9d5a362559265  |    2 +
 .../testdata/fuzz/FuzzIdentifier/28464299ac9ceaf3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e18a0fadf8f8d1e  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e6e50da573e69b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/33e9c7c26e121287  |    2 +
 .../testdata/fuzz/FuzzIdentifier/348f655cbe635cf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34b4da777d929731  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34e7a76efc318e82  |    2 +
 .../testdata/fuzz/FuzzIdentifier/35351672b96f8693  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3695e57e46ce93c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/37ee41c42fb1c3c8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3903663d1e91caeb  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3c4eb8315991acf7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3d501ac59ad587c9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3db44c7ba19fdd16  |    2 +
 .../testdata/fuzz/FuzzIdentifier/436a22ea064a03b1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4415ff0d1b9299f1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/445fca83849180f2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/45a37a7d87af8888  |    2 +
 .../testdata/fuzz/FuzzIdentifier/46fa4fad84220ea4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/471c839522bc6d06  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4935e94ff365ef93  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ddf220b34d80659  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ee3ead78b5e68a2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5288346b2319478f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5438070243513251  |    2 +
 .../testdata/fuzz/FuzzIdentifier/54811be07baf792d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/56065070b35266a8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/58540c400d7a154c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5ac14d4f3feba852  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b47e5ee0596ab19  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b4f050cad7d6971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5d03424fa48149c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5df9ba84197819df  |    2 +
 .../testdata/fuzz/FuzzIdentifier/60aff40e01913abe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66542488bb28dac3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66889c986f27ea1d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/677dfe3de201697f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6a16db612d198990  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6ad80d0a2c06ac54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6b16cbabe8e4a2e7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6c6dfd24f8c73c0b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/76b6d045b42add72  |    2 +
 .../testdata/fuzz/FuzzIdentifier/77fa2102462675e0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7ad7e6cfe18eb7fe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7b4af4ca74813068  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7daed2d2a835f0b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e151b9450d2b761  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e2babaa1ef35360  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8059934d90bcf246  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8378d0fed0b8996b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8ac7c327fd5b5cc3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8dd1afcdc5104588  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8fff7f2dd3835c42  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9006884cc2246a54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/90c2eff4ee6601b9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/92bbc2ba64e693c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9512693f2cee29c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9633d0ce6f89b127  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9789b6bb44075878  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9a89fb10b388482a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c3612f616e75c30  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c83e3607e077307  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9f72fab7a7931bc4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a4ce8510de4da361  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a5833806503a0d20  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a595c2930c69a903  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ab73d083e8e43971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ad4c4ad6237bb964  |    2 +
 .../testdata/fuzz/FuzzIdentifier/af9e1ebfb8d2795d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b128ad1323911a0d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b3feba13964d5945  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b41d082073a2bebc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b48b96f658bda199  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b6073b717a32efea  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b70f41cf6885f569  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b8dadd1180c17fc2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bc4962e0586168dd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bee82b5732ad61c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c59644d3f3881a96  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c61c2d3db1f59301  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ca57023a06a80b6f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cb2b19dce8ab4792  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cbb1fd2d98956b9f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cea400674b589d15  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cef254100bb1d3ec  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cf37f09b95c8bad1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d10d0ab0376ef2c6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d15249b25edd04e1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d1a9e9faf54d8eb4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d43cc2dfd7b882e3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dc8d3d87a20bf6bd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dcad439aaec6c2f6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dd271580db144444  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e257a0a06a6d0ddd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2927b2f113531c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2b8af39f88d630c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e43666abfc98f368  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e55caa61264144d4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e758baca4af7c7b3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ec1cb26f1a43547c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ee4745c7324edeaf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f080234fd9c3be57  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f34f2012d2974cb0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f3a15bf1ad31abb1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f5b1055fe63bbb4f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f6c3d89cb56eb2f0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f85456d0b9f26fbe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd22c094ae02673c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd474a797a00ff71  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fe2500ec53808c61  |    2 +
 internal/sqlbuild/testdata/mysql/delete_drop.sql   |    1 +
 .../testdata/mysql/delete_drop_cascade.sql         |    1 +
 internal/sqlbuild/testdata/mysql/delete_rows.sql   |    2 +
 .../sqlbuild/testdata/mysql/delete_truncate.sql    |    1 +
 .../testdata/mysql/delete_truncate_restart.sql     |    1 +
 internal/sqlbuild/testdata/mysql/insert.sql        |    2 +
 .../testdata/mysql/insert_skip_conflict.sql        |    2 +
 internal/sqlbuild/testdata/mysql/select.sql        |    3 +
 internal/sqlbuild/testdata/mysql/select_all.sql    |    2 +
 internal/sqlbuild/testdata/mysql/select_ilike.sql  |    3 +
 internal/sqlbuild/testdata/mysql/select_null.sql   |    3 +
 .../sqlbuild/testdata/mysql/select_ordered.sql     |    2 +
 internal/sqlbuild/testdata/mysql/update.sql        |    2 +
 internal/sqlbuild/testdata/mysql/upsert.sql        |    2 +
 .../sqlbuild/testdata/mysql/upsert_nothing.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_drop.sql     |    1 +
 .../testdata/postgres/delete_drop_cascade.sql      |    1 +
 .../sqlbuild/testdata/postgres/delete_rows.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_truncate.sql |    1 +
 .../testdata/postgres/delete_truncate_restart.sql  |    1 +
 internal/sqlbuild/testdata/postgres/insert.sql     |    3 +
 .../testdata/postgres/insert_skip_conflict.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select_all.sql |    2 +
 .../sqlbuild/testdata/postgres/select_ilike.sql    |    3 +
 .../sqlbuild/testdata/postgres/select_null.sql     |    3 +
 .../sqlbuild/testdata/postgres/select_ordered.sql  |    2 +
 internal/sqlbuild/testdata/postgres/update.sql     |    3 +
 internal/sqlbuild/testdata/postgres/upsert.sql     |    3 +
 .../sqlbuild/testdata/postgres/upsert_nothing.sql  |    3 +
 internal/sqlguard/admit.go                         |  245 ++
 internal/sqlguard/attack_test.go                   |  344 ++
 internal/sqlguard/dialect.go                       |  260 ++
 internal/sqlguard/doc.go                           |   53 +
 internal/sqlguard/sqlguard.go                      |  443 +++
 internal/sqlguard/sqlguard_test.go                 |  338 ++
 internal/sqlnode/export_test.go                    |   11 +
 internal/sqlnode/guard_test.go                     |  126 +
 internal/sqlnode/introspect.go                     |  240 ++
 internal/sqlnode/sqlnode.go                        |  745 +++-
 internal/sqlnode/sqlnode_test.go                   |  106 +
 internal/web/dist/index.html                       |   38 +-
 internal/webhook/webhook.go                        |   74 +
 internal/webhook/webhook_test.go                   |  179 +-
 internal/workflow/compiler.go                      |  145 +-
 internal/workflow/compiler_test.go                 |  118 +
 internal/workflow/lifecycle.go                     |   51 +
 migrations/.gitkeep                                |    0
 migrations/embed.go                                |   27 +
 migrations/postgres/000001_baseline.down.sql       |   23 +
 migrations/postgres/000001_baseline.up.sql         |  192 +
 .../postgres/000002_workflow_history.down.sql      |   11 +
 migrations/postgres/000002_workflow_history.up.sql |   30 +
 migrations/postgres/000003_identity.down.sql       |   15 +
 migrations/postgres/000003_identity.up.sql         |   75 +
 .../postgres/000004_execution_indexes.down.sql     |    5 +
 .../postgres/000004_execution_indexes.up.sql       |   26 +
 migrations/sqlite/000001_baseline.down.sql         |   22 +
 migrations/sqlite/000001_baseline.up.sql           |  185 +
 migrations/sqlite/000002_workflow_history.down.sql |   11 +
 migrations/sqlite/000002_workflow_history.up.sql   |   29 +
 migrations/sqlite/000003_identity.down.sql         |   14 +
 migrations/sqlite/000003_identity.up.sql           |   73 +
 .../sqlite/000004_execution_indexes.down.sql       |    5 +
 migrations/sqlite/000004_execution_indexes.up.sql  |   21 +
 nodes/ai.go                                        | 1238 ++++++-
 nodes/ai_ollama_test.go                            |  413 +++
 nodes/ai_test.go                                   | 1435 +++++++-
 nodes/apostrophe_live_test.go                      |   43 +
 nodes/code.go                                      |  114 +-
 nodes/code_test.go                                 |   91 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  170 +-
 nodes/database.go                                  |  307 +-
 nodes/database_test.go                             |  706 +++-
 nodes/datetime.go                                  |  408 +++
 nodes/datetime_test.go                             |  274 ++
 nodes/executors.go                                 |  576 ++-
 nodes/executors_test.go                            |  151 +
 nodes/flow.go                                      |  457 +++
 nodes/flow_test.go                                 |  464 +++
 nodes/http.go                                      |    4 +-
 nodes/jscode.go                                    |  172 +
 nodes/jscode_test.go                               |  100 +
 nodes/mysql_v2.go                                  |  199 +
 nodes/mysql_v2_test.go                             |  181 +
 nodes/postgres_v2.go                               |  633 ++++
 nodes/postgres_v2_test.go                          |  231 ++
 nodes/sql_options.go                               |  569 +++
 nodes/sql_options_live_test.go                     |  594 +++
 nodes/sql_options_test.go                          |  257 ++
 nodes/sqlite_attach_test.go                        |  161 +
 nodes/subworkflow.go                               |  275 ++
 nodes/testdata/n8n_chat_model_options.json         |   38 +
 nodes/testdata/n8n_sql_options.json                |   17 +
 nodes/transform.go                                 |  745 ++++
 nodes/transform_test.go                            |  315 ++
 nodes/wait.go                                      |  230 ++
 nodes/webhook.go                                   |  257 +-
 scripts/docker-tags.sh                             |   84 +
 scripts/smoke-docker.sh                            |   13 +-
 scripts/smoke-postgres.sh                          |   43 +-
 sdk/README.md                                      |   25 +-
 sdk/package.json                                   |    2 +-
 sdk/src/generated/models.ts                        |  960 ++++-
 sdk/src/version.ts                                 |   15 +-
 web/src/lib/api/generated/auth/auth.ts             |  752 ++++
 .../lib/api/generated/credentials/credentials.ts   |  105 +-
 web/src/lib/api/generated/models/aPIKeyResource.ts |   20 +
 .../api/generated/models/createAPIKeyInputBody.ts  |   17 +
 .../models/createStreamTicketInputBody.ts          |   17 +
 .../api/generated/models/createdAPIKeyResource.ts  |   18 +
 web/src/lib/api/generated/models/definition.ts     |    1 +
 .../lib/api/generated/models/executionResource.ts  |    2 +
 .../lib/api/generated/models/executionSummary.ts   |    2 +
 web/src/lib/api/generated/models/index.ts          |   20 +
 .../api/generated/models/listAPIKeysOutputBody.ts  |   15 +
 .../generated/models/listWorkflowVersionsParams.ts |   20 +
 .../api/generated/models/loadOptionsInputBody.ts   |    2 +
 .../lib/api/generated/models/loadSchemaResource.ts |   17 +
 web/src/lib/api/generated/models/loginInputBody.ts |   24 +
 web/src/lib/api/generated/models/mapperColumn.ts   |   21 +
 .../lib/api/generated/models/principalResource.ts  |   24 +
 .../lib/api/generated/models/propertyDefinition.ts |    5 +
 web/src/lib/api/generated/models/propertyMode.ts   |   19 +
 .../generated/models/publishVersionInputBody.ts    |   17 +
 .../generated/models/resourceMapperDeclaration.ts  |   15 +
 .../api/generated/models/streamTicketResource.ts   |   16 +
 .../api/generated/models/testCredentialResource.ts |    3 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 .../models/workflowPublishEventResource.ts         |   19 +
 .../models/workflowPublishEventResourceAction.ts   |   16 +
 .../models/workflowVersionListResource.ts          |   17 +
 .../models/workflowVersionSummaryResource.ts       |   23 +
 web/src/lib/api/generated/nodes/nodes.ts           |  103 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |  314 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  113 +-
 web/src/lib/api/http.test.ts                       |   79 +-
 web/src/lib/api/http.ts                            |   23 +
 .../lib/components/dashboard/list-states.svelte    |  136 +
 web/src/lib/components/ui/table/index.ts           |   28 +
 web/src/lib/components/ui/table/table-body.svelte  |   15 +
 .../lib/components/ui/table/table-caption.svelte   |   20 +
 web/src/lib/components/ui/table/table-cell.svelte  |   15 +
 .../lib/components/ui/table/table-footer.svelte    |   20 +
 web/src/lib/components/ui/table/table-head.svelte  |   15 +
 .../lib/components/ui/table/table-header.svelte    |   20 +
 web/src/lib/components/ui/table/table-row.svelte   |   15 +
 web/src/lib/components/ui/table/table.svelte       |   17 +
 .../workflow-editor/activation-notices.svelte      |  120 +
 .../components/workflow-editor/canvas-node.svelte  |   12 +
 .../workflow-editor/properties-panel.svelte        |   27 +-
 .../workflow-editor/property-field.svelte          |  407 ++-
 .../workflow-editor/version-panel.svelte           |  509 +++
 .../workflow-editor/workflow-editor.svelte         |  286 +-
 web/src/lib/dashboard/cursor-page.test.ts          |   71 +
 web/src/lib/dashboard/cursor-page.ts               |   64 +
 web/src/lib/dashboard/list-state.test.ts           |   65 +
 web/src/lib/dashboard/list-state.ts                |   69 +
 web/src/lib/dashboard/request-guard.test.ts        |   55 +
 web/src/lib/dashboard/request-guard.ts             |   44 +
 web/src/lib/embed/embed-editor.svelte              |   50 +-
 web/src/lib/embed/session.svelte.ts                |   22 +-
 web/src/lib/embed/session.test.ts                  |   35 +-
 web/src/lib/workflow-editor/activation.test.ts     |  130 +
 web/src/lib/workflow-editor/activation.ts          |  108 +
 web/src/lib/workflow-editor/collection.test.ts     |  131 +
 web/src/lib/workflow-editor/collection.ts          |  148 +
 web/src/lib/workflow-editor/conditions.test.ts     |   82 +
 web/src/lib/workflow-editor/conditions.ts          |   80 +
 web/src/lib/workflow-editor/document.ts            |   10 +-
 .../lib/workflow-editor/fixed-collection.test.ts   |  131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |   75 +
 web/src/lib/workflow-editor/history-diff.test.ts   |  273 ++
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 0 -> 10556 bytes
 web/src/lib/workflow-editor/node-visual.ts         |   14 +
 .../lib/workflow-editor/resource-locator.test.ts   |  112 +
 web/src/lib/workflow-editor/resource-locator.ts    |  100 +
 .../lib/workflow-editor/resource-mapper.test.ts    |  121 +
 web/src/lib/workflow-editor/resource-mapper.ts     |  111 +
 .../lib/workflow-editor/version-history.test.ts    |  191 +
 web/src/lib/workflow-editor/version-history.ts     |  156 +
 web/src/lib/workflow-editor/visibility.ts          |   20 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |  141 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  113 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   54 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  190 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    7 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   54 +-
 web/src/routes/+page.svelte                        |    8 +-
 581 files changed, 71256 insertions(+), 1966 deletions(-)
```
