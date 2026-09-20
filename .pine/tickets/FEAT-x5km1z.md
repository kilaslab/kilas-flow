---
id: FEAT-x5km1z
title: 'Auth-enabled dashboard unusable: no sign-in page, no 401 handling, empty Settings'
status: done
priority: high
labels:
    - auth
    - ui
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T02:04:08Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 7 finding(s) from dims: find:ui-ops-surfaces, find:ui-shell-lists, find:unfinished-work, find:web-frontend-code.

---
### Auth-enabled deployments have no sign-in page: the dashboard renders and then every call fails with 401 [find:ui-ops-surfaces] (critical/unfinished) · area: auth / dashboard shell · confidence: high

With KILASFLOW_AUTH_ENABLED=true the SPA has no login route, no redirect on 401, no sign-out and no current-user display. Every page renders its shell and then shows "401 — This request needs an API key or a signed-in session".

Evidence: Private instance on :18099 with auth on, fresh browser. /app/workflows renders the full shell (Import n8n, New workflow) and its lists fail. /executions shows "Executions could not be loaded — 401 …" (17-auth-on-executions.png). /login returns "404 Not Found". Grepping web/src for auth/login, authLogin, logout and auth/me finds no caller outside the generated client. The only way in is POST /api/v1/auth/login with curl to get the kilasflow_session cookie. FEAT-ddzk2k explicitly deferred "the SPA login route with a 401 redirect", and no follow-up ticket exists.

n8n behavior: An unauthenticated visit redirects to /signin, and the user menu offers sign-out.

Impact: Nobody can use the editor on a production (auth-on) instance through the UI.

Suggested fix: Add a /signin route that uses POST /api/v1/auth/login. Make lib/api/http.ts send any 401 to /signin?next=…. Add a user menu (auth/me, logout) to the (dashboard) layout.

Files: web/src/routes/(dashboard)/+layout.svelte, web/src/lib/api/http.ts, web/src/routes (new signin route)

Existing tickets: FEAT-ddzk2k

---
### Enabling authentication makes the dashboard unusable: no login page, no 401 handling, no API-key or user UI; Settings is an empty placeholder [find:unfinished-work] (high/unfinished) · area: web / auth · confidence: high

The server implements sessions, API keys and tenancy behind auth.enabled, and the docs tell operators to enable it. The SPA never calls any auth endpoint and has no login route. Once identity is on, every dashboard API call gets 401 and the user has no way to sign in.

Evidence: The API has POST /api/v1/auth/login|logout, GET /api/v1/auth/me and /api/v1/api-keys, and config.example.yaml:130-160 describes 'SessionTTL is how long a dashboard login lasts'. A grep of web/src (excluding generated/) finds no reference to auth/login, auth/me or api-keys. web/src/lib/api/http.ts has no 401 branch. There is no login route. web/src/routes/(dashboard)/settings/+page.svelte is 11 lines: 'No settings are available in this shell yet.' FEAT-ddzk2k Work evidence (line 114): 'The SPA login route and the SDK's apiKey convenience are not in this change'. No follow-up ticket exists; the same note says account lockout 'is worth its own ticket', and none exists. docs/operate/security.md tells operators to turn identity on. Not tested live: no private port was available to start an auth-enabled instance.

n8n behavior: Sign-in page, user management, and personal API keys under Settings.

Impact: The recommended production posture (auth on) locks operators out of the editor. API keys can only be managed with curl.

Suggested fix: Add a /login route, redirect to it on 401, add sign-out, and add Settings pages for API keys (create/revoke, show once) and the account. Open a ticket for login rate-limiting and lockout.

Files: web/src/lib/api/http.ts, web/src/routes/(dashboard)/settings/+page.svelte, web/src/routes/+layout.svelte, internal/api/middleware/auth.go

Existing tickets: FEAT-ddzk2k

---
### The SPA has no sign-in route and no 401 handling, so the dashboard is unusable when auth is enabled [find:web-frontend-code] (high/unfinished) · area: auth UI · confidence: high

When auth is on, the server gates /api/v1 and returns 401 'This request needs an API key or a signed-in session.' It also provides /auth/login, /auth/logout, /auth/me and /stream-tickets. The SPA never calls any of these: no login route, no global 401 redirect, no logout or user menu. EventSource does not use stream tickets. Every page would just show '401 — ...' errors.

Evidence: internal/api/middleware/auth.go:67-105 (gate). The generated operations login, logout, getMe and createStreamTicket have 0 call sites outside /generated/. web/src/routes contains only (dashboard), approve and embed. web/src/lib/api/http.ts treats 401 as a generic ApiError. FEAT-ddzk2k (done) line 114: 'The SPA login route and the SDK's apiKey convenience are **not** in this change', and no follow-up ticket exists. Static evidence only; not checked on a live auth instance.

n8n behavior: n8n shows a sign-in page and redirects there on 401, and has a user menu with sign out.

Impact: Operators cannot turn on authentication and still use the built-in dashboard, which blocks any multi-user or production deployment of the editor.

Suggested fix: Add a /login route that posts email and password to /auth/login. Add a 401 interceptor in apiFetch or a QueryCache onError that redirects with returnTo. Add a user menu with logout. Mint a stream ticket per EventSource connect.

Files: /Users/izzadev/projects/k-flow/web/src/lib/api/http.ts, /Users/izzadev/projects/k-flow/web/src/lib/query-client.ts, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/event-stream.svelte.ts, /Users/izzadev/projects/k-flow/internal/api/middleware/auth.go

Existing tickets: FEAT-ddzk2k

---
### Settings is an empty placeholder, while API keys and 'who am I' have a complete API but no UI [find:ui-shell-lists] (medium/unfinished) · area: settings / IA · confidence: high

/settings shows only 'No settings are available in this shell yet.' The server provides GET/POST/DELETE /api/v1/api-keys (create returns a token shown once) and GET /auth/me, but nothing in web/src calls them. Operators have to use curl to create or revoke API keys. There is also no instance information (version, public URL, timezone) and no profile section. This may overlap ui-ops-surfaces, whose plan includes settings and API keys.

Evidence: routes/(dashboard)/settings/+page.svelte is 11 lines. Searching web/src outside api/generated for createApiKey, listApiKeys or getMe finds nothing. openapi has list-api-keys, create-api-key and revoke-api-key.

Impact: Embedding customers need API keys for server-to-server calls and cannot get them in the product.

Suggested fix: Add Settings sections: API keys (list with prefix, label and last used; create with a copy-once dialog; revoke with confirm), Profile (from /auth/me, with sign out), and About (version from /health, public URL, timezone).

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/settings/+page.svelte, /Users/izzadev/projects/k-flow/internal/api/handlers/auth.go

---
### Settings page is an empty placeholder: no UI for API keys, account or instance info [find:ui-ops-surfaces] (high/unfinished) · area: settings · confidence: high

The API key endpoints exist (GET/POST /api-keys, DELETE /api-keys/{id}), but Settings only says "No settings are available in this shell yet." There is also no profile, sign-out or version page.

Evidence: routes/(dashboard)/settings/+page.svelte is 11 lines of placeholder text (18-settings.png). GET /api/v1/api-keys returns 200 {"items":[]} on :8090, and on :18099 with a session. There is no caller in web/src outside the generated client.

n8n behavior: Settings has an n8n API page (create a key with label/expiry, copy once, delete), plus Personal, Users and other sections.

Impact: On auth-on deployments, getting an API key for the SDK, a host backend or CI needs curl plus a session cookie. Keys cannot be listed, audited or revoked in the product.

Suggested fix: Add Settings sections: API keys (list with prefix/created/last used; create with name/expiry and a show-once secret; revoke with confirmation), Account (auth/me, sign out), and About (version from /health) with read-only instance config (timezone, retention, embed origins).

Files: web/src/routes/(dashboard)/settings/+page.svelte

Existing tickets: FEAT-ddzk2k

---
### Enabling authentication makes the dashboard unusable: no login page, no 401 handling, no API-key or user UI; Settings is an empty placeholder [find:unfinished-work] (high/unfinished) · area: web / auth · confidence: high

The server implements sessions, API keys and tenancy behind auth.enabled, and the docs tell operators to enable it. The SPA never calls any auth endpoint and has no login route. Once identity is on, every dashboard API call gets 401 and the user has no way to sign in.

Evidence: The API has POST /api/v1/auth/login|logout, GET /api/v1/auth/me and /api/v1/api-keys, and config.example.yaml:130-160 describes 'SessionTTL is how long a dashboard login lasts'. A grep of web/src (excluding generated/) finds no reference to auth/login, auth/me or api-keys. web/src/lib/api/http.ts has no 401 branch. There is no login route. web/src/routes/(dashboard)/settings/+page.svelte is 11 lines: 'No settings are available in this shell yet.' FEAT-ddzk2k Work evidence (line 114): 'The SPA login route and the SDK's apiKey convenience are not in this change'. No follow-up ticket exists; the same note says account lockout 'is worth its own ticket', and none exists. docs/operate/security.md tells operators to turn identity on. Not tested live: no private port was available to start an auth-enabled instance.

n8n behavior: Sign-in page, user management, and personal API keys under Settings.

Impact: The recommended production posture (auth on) locks operators out of the editor. API keys can only be managed with curl.

Suggested fix: Add a /login route, redirect to it on 401, add sign-out, and add Settings pages for API keys (create/revoke, show once) and the account. Open a ticket for login rate-limiting and lockout.

Files: web/src/lib/api/http.ts, web/src/routes/(dashboard)/settings/+page.svelte, web/src/routes/+layout.svelte, internal/api/middleware/auth.go

Existing tickets: FEAT-ddzk2k

---
### API features with no UI: delete workflow, cancel execution, API keys, credential test; Settings is a placeholder [find:web-frontend-code] (medium/unfinished) · area: dashboard · confidence: high

The OpenAPI exposes DELETE /workflows/{id}, POST /executions/{id}/cancel, /api-keys CRUD and credential test endpoints. The generated clients deleteWorkflow, cancelExecution, listApiKeys, createApiKey, revokeApiKey and testCredential have 0 call sites in web/src. The Settings page reads 'No settings are available in this shell yet'. The workflow list has no delete, duplicate, rename, search or sort, over an unpaginated list (636 rows on the shared instance).

Evidence: grep of generated operation names outside /generated/ gives 0 for each. routes/(dashboard)/settings/+page.svelte (placeholder). routes/(dashboard)/app/workflows/+page.svelte (list with only an Activate toggle). routes/(dashboard)/executions/[id]/+page.svelte has no Stop control for running or waiting executions.

n8n behavior: n8n offers workflow delete/archive and duplicate, a Stop execution button, API key management in Settings, and credential Test.

Impact: Users cannot remove workflows, stop runaway or waiting executions, or issue API keys without calling the API by hand.

Suggested fix: Wire the existing endpoints: row menu (Delete / Duplicate / Rename), Stop button on running executions, Settings > API keys, and credential Test.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/executions/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/settings/+page.svelte

## Acceptance criteria

- [ ] Auth-enabled deployments have no sign-in page: the dashboard renders and then every call fails with 401
- [ ] Enabling authentication makes the dashboard unusable: no login page, no 401 handling, no API-key or user UI; S
- [ ] The SPA has no sign-in route and no 401 handling, so the dashboard is unusable when auth is enabled
- [ ] Settings is an empty placeholder, while API keys and 'who am I' have a complete API but no UI
- [ ] Settings page is an empty placeholder: no UI for API keys, account or instance info
- [ ] Enabling authentication makes the dashboard unusable: no login page, no 401 handling, no API-key or user UI; S
- [ ] API features with no UI: delete workflow, cancel execution, API keys, credential test; Settings is a placehold
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (2):
  - `8ab42f7e` — FEAT-x5km1z: /login route, approved 401 redirect (embed/auth guards), shell user menu, Settings account+API keys+about
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   11 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  921 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 +++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  669 +++++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++++
 .pine/tickets/BUG-6jvcs5.md                        |  689 ++++++
 .pine/tickets/BUG-8dmp5y.md                        |  639 +++++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++++
 .pine/tickets/BUG-8sb0jw.md                        |  695 ++++++
 .pine/tickets/BUG-8t94wn.md                        |  628 +++++
 .pine/tickets/BUG-9853ay.md                        |  534 ++++
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  794 ++++++
 .pine/tickets/BUG-c241hm.md                        |  607 +++++
 .pine/tickets/BUG-cq4yk3.md                        |  795 ++++++
 .pine/tickets/BUG-dndnhn.md                        |  497 ++++
 .pine/tickets/BUG-esb9sh.md                        |  590 +++++
 .pine/tickets/BUG-f9frth.md                        |  870 +++++++
 .pine/tickets/BUG-fv5fer.md                        |  635 +++++
 .pine/tickets/BUG-gaavr5.md                        |  813 ++++++
 .pine/tickets/BUG-hfhzq6.md                        |  501 ++++
 .pine/tickets/BUG-hm76dq.md                        |  569 +++++
 .pine/tickets/BUG-j7rtv3.md                        |  137 +
 .pine/tickets/BUG-kzkvv6.md                        |  524 ++++
 .pine/tickets/BUG-mewhrd.md                        |  517 ++++
 .pine/tickets/BUG-mz8xrb.md                        |  506 ++++
 .pine/tickets/BUG-npfz43.md                        |  487 ++++
 .pine/tickets/BUG-pwckhd.md                        |  512 ++++
 .pine/tickets/BUG-qmgz2f.md                        |  630 +++++
 .pine/tickets/BUG-qq4xva.md                        |  506 ++++
 .pine/tickets/BUG-rjd6fm.md                        |  721 ++++++
 .pine/tickets/BUG-rrkjrd.md                        |  526 ++++
 .pine/tickets/BUG-s0wy50.md                        |  507 ++++
 .pine/tickets/BUG-t2wezf.md                        |  536 ++++
 .pine/tickets/BUG-tcqkad.md                        |  639 +++++
 .pine/tickets/BUG-txc9xg.md                        |  520 ++++
 .pine/tickets/BUG-wdypd2.md                        |  680 +++++
 .pine/tickets/BUG-wp2y0y.md                        |  495 ++++
 .pine/tickets/BUG-xf1wqm.md                        |  493 ++++
 .pine/tickets/BUG-y57cz4.md                        |  617 +++++
 .pine/tickets/BUG-ysvmaa.md                        |  752 ++++++
 .pine/tickets/BUG-ze1nn8.md                        |  564 +++++
 .pine/tickets/BUG-ztzxck.md                        |  507 ++++
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  761 ++++++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  625 +++++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  642 +++++
 .pine/tickets/FEAT-j5s2n4.md                       |  639 +++++
 .pine/tickets/FEAT-jvembs.md                       |  813 ++++++
 .pine/tickets/FEAT-nqpvf6.md                       |  611 +++++
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 .pine/tickets/FEAT-x5km1z.md                       |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |  114 +-
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
 docs/src/content/docs/start/install.md             |   44 +-
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
 internal/ai/agent.go                               |  101 +-
 internal/ai/ai.go                                  |   31 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  140 +-
 internal/ai/openai_test.go                         |  145 ++
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  102 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/embed_confinement_test.go             |  284 +++
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 +++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   37 +-
 internal/api/handlers/datastores.go                |   32 +-
 internal/api/handlers/datastores_csv.go            |   72 +
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  318 ++-
 internal/api/handlers/interop.go                   |  130 +-
 internal/api/handlers/problem.go                   |   31 +
 internal/api/handlers/schedules.go                 |   35 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  132 +
 internal/api/middleware/cors_test.go               |  189 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |  123 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  257 ++
 internal/config/config.go                          |  305 ++-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   16 +
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
 internal/engine/runner.go                          | 1670 +++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  724 +++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  174 +-
 internal/engine/wait_service_test.go               |  232 +-
 internal/engine/worker_test.go                     |   15 +
 internal/expression/doc.go                         |   94 +-
 internal/expression/evaluator.go                   |  771 ++++++
 internal/expression/expression.go                  |  474 +---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  528 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1187 +++++++++
 internal/expression/parity_test.go                 |  505 ++++
 internal/expression/parser.go                      |  824 +++++++
 internal/expression/roots.go                       |  339 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         |  772 ++++++
 internal/interop/n8n/n8n.go                        |  462 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2606 ++++++++++++++++++--
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
 internal/repository/executions.go                  |  534 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   40 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  153 ++
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
 internal/webhook/request_lifecycle.go              |  425 +++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  758 ++++--
 internal/webhook/webhook_test.go                   |  678 ++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/ai.go                                        |  498 +++-
 nodes/ai_test.go                                   |  733 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  220 ++
 nodes/embedscope_test.go                           |  242 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  347 ++-
 nodes/http_test.go                                 |  302 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |   97 +-
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  477 +++-
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
 .../workflow-editor/property-field.svelte          |  534 ++--
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  736 +++++-
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  151 +-
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
 web/src/lib/workflow-editor/document.ts            |  291 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   50 +
 web/src/lib/workflow-editor/expression-assist.ts   |   91 +
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
 web/src/lib/workflow-editor/shortcuts.test.ts      |   77 +
 web/src/lib/workflow-editor/shortcuts.ts           |  115 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  369 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  369 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  147 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    6 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  176 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   59 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  293 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 434 files changed, 77358 insertions(+), 4725 deletions(-)
```
