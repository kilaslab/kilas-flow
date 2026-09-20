---
id: BUG-esb9sh
title: 'Ops credentials/datastore/schedules UI: forms, validation, test, grid editing, framing, markers'
status: done
priority: medium
labels:
    - ui
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:04:00Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 7 finding(s) from dims: find:ui-ops-surfaces, find:web-frontend-code.

---
### Credential form ignores type defaults (Telegram baseUrl), accepts invalid values, and has no Test button although the test API exists [find:ui-ops-surfaces] (medium/ux) · area: credentials · confidence: high

Creating a Telegram credential fails with 422 because the Base URL default is never filled in. Every field is a plain text or password input, invalid values such as port "abc" are saved, and the existing test endpoints are never used.

Evidence: GET /credential-types: telegramApi.baseUrl is required with default "https://api.telegram.org". In the UI, New credential → Telegram leaves Base URL empty, and Save shows "422 — credential field \"baseUrl\" is required" (13-telegram-baseurl-refused.png). openCreate() sets fields = {} and never reads field.default. POST /credentials for postgres with {port:"abc", sslMode:"banana"} returned 201. POST /credentials/{id}/test on it answers {"ok":false,"detail":"database port \"abc\" is not a port number"}, but no UI calls the test endpoints. The list shows raw type ids such as httpBearerAuth. Only 10 credential types exist, against 47 used by the top 100 templates (including googleSheetsOAuth2Api ×21 and gmailOAuth2 ×10), and there is no OAuth2 flow.

n8n behavior: The credential modal fills in defaults, uses typed inputs and dropdowns, and tests on save with a success or error banner and Retry.

Impact: The Telegram credential (used by 16 of the 100 templates) cannot be created without knowing the API URL, and database or API credentials go unchecked until a workflow fails.

Suggested fix: Fill fields from field.default on create and on type change. Add a Test button, plus test-on-save, using POST /credential-types/{type}/test and /credentials/{id}/test. Add field kind/options/placeholder metadata and render typed inputs (e.g. an SSL mode select, number for port). Show displayName in the list.

Files: web/src/routes/(dashboard)/credentials/+page.svelte, internal/credentials

Existing tickets: FEAT-snxxny

---
### Schedules page: schedules for inactive workflows show "Active · Next …" then pause silently; trigger-owned schedules can be edited; the UTC claim is wrong for them [find:ui-ops-surfaces] (medium/ux) · area: schedules · confidence: high

You can save an Active schedule for a workflow that is not activated, and the list shows a next run time. At the due time the scheduler quietly sets it inactive with no record or notice. Schedules created by Schedule Trigger nodes look like manual ones and can be edited or deleted, which puts them out of step with the node. The page says times are UTC, but trigger schedules follow the workflow's timezone.

Evidence: (a) "[ui-ops-surfaces] failing http" (not activated) got a UI schedule "0 9 * * 1-5", shown as "Active … Next Sep 18, 05:00 PM" (16-schedules.png). GET /schedules now returns active:false, updatedAt 2026-09-18T09:12:35Z, no nextRunAt, and there are no schedule-triggered executions. internal/repository/schedules.go ClaimDue sets active=false and next_run_at=nil when the parent workflow is inactive. (b) The trigger-owned row (nodeId "s1") accepts PUT /schedules/{id} with 200, and the page ignores nodeId. (c) Importing a scheduleTrigger (triggerAtHour 9, settings.timezone Asia/Jakarta) gives cron "0 9 * * *" and nextRunAt 02:00Z, which is correct, yet the header says "Times are evaluated in UTC", rows show no timezone, and manual schedules have no timezone field. (d) Delete is one click with errors hidden. (e) Schedules are cron text only, with no link to their executions.

n8n behavior: Schedules exist only as Schedule Trigger nodes (interval modes or cron), run in the workflow or instance timezone, and fire only when the workflow is active.

Impact: Operators think a schedule is armed when it will never fire. Edits to trigger-owned rows contradict the canvas and can be lost.

Suggested fix: Refuse schedules for inactive workflows, or mark them "Won't run — not activated", and show why a schedule was paused. Make trigger-owned rows read-only with an "Open in editor" link. Show a timezone per schedule, confirm deletes, and link each row to its executions.

Files: web/src/routes/(dashboard)/schedules/+page.svelte, internal/repository/schedules.go, internal/api/handlers/schedules.go

---
### Datastore grid supports reading and adding only: no cell editing, search, filter, sort or total count, although the API supports update and filters [find:ui-ops-surfaces] (medium/parity-gap) · area: datastores/[id] grid · confidence: high

Existing rows cannot be edited in the UI. The grid has no search, filter or sort and no total count, just "Load more" 20 rows at a time. Date and boolean values are shown inconsistently, and CSV import cannot create columns.

Evidence: On /datastores/datastore_01a0af2c-… (4 typed columns, 50 rows), double-clicking a cell does nothing (activeElement stays MAIN, 0 inputs). The page has no search or filter control and no total (20-datastore-grid.png). The page imports only insert/list/delete rows and add/rename/delete column; it never calls updateDatastoreRows (PUT /rows) or the list filters (match/columnName/condition/value) that the API has. User datetime values appear as raw ISO while createdAt/updatedAt are localized, and booleans are plain text. POST /rows/import on a datastore without columns returns 422 "csv import header names unknown column \"name\"". The export filename uses the datastore id rather than its name.

n8n behavior: Data tables (design-refs/n8n-v2/23, 24) have inline cell editing, a + row, a header search box, column type icons, "Total N" and a 20/page selector.

Impact: Fixing one wrong value in a lookup or dedup table needs the API or a delete and re-add, and large tables cannot be navigated.

Suggested fix: Add inline, type-aware editing through PUT /rows, a search box and column filters on the existing list parameters, server-side column sort, a total count with a page-size selector, consistent formatting, and an option to create missing columns from the CSV header.

Files: web/src/routes/(dashboard)/datastores/[id]/+page.svelte, internal/api/handlers (datastores)

Existing tickets: FEAT-agj52c

---
### No framing policy on any page: /embed is not limited to the configured embed origins, and the dashboard and /approve can be framed by any site [find:ui-ops-surfaces] (medium/security) · area: embed / approve hardening · confidence: high

The SPA sends no Content-Security-Policy frame-ancestors and no X-Frame-Options header. The embed's host check happens only in the browser (document.referrer), and the unauthenticated approval page with its Approve/Reject buttons can be framed for clickjacking.

Evidence: `curl -D -` against :18099 for /embed/{id}, /app/workflows and /approve/x shows no CSP and no X-Frame-Options; only internal/api/docs.go sets a CSP. lib/embed/session.svelte.ts parentOrigin() trusts document.referrer, and embed.allowed_origins never reaches the browser as a frame policy. The session cookie is SameSite=Lax, which blocks cross-site framing with the cookie but not same-site framing.

Impact: The white-label embed and the approval page lack a defense-in-depth layer.

Suggested fix: Send frame-ancestors <embed.allowed_origins> on /embed/*, and frame-ancestors 'none' plus X-Frame-Options: DENY on the dashboard and /approve/*.

Files: internal/web (SPA handler), internal/api/server.go, web/src/lib/embed/session.svelte.ts

---
### Credentials, Schedules and Datastores lists: fixed-height rows overlap long content, and the page header overflows on phones [find:ui-ops-surfaces] (low/ui) · area: credentials / schedules / datastores list layout · confidence: high

Rows are h-11 and the second line (type and allowed hosts) is not truncated, so it spills into the next row. At 390 px wide, the header keeps the title and the w-full button on one line, causing horizontal scroll.

Evidence: 12-credentials-list.png and 15-cred-delete-failure-silent.png (1440 px) and 27-credentials-mobile.png (390 px) show overlapping rows. At 390 px, document.scrollWidth is 474 on credentials, 467 on schedules and 470 on datastores; executions and settings stay at 390.

Impact: Rows for credentials scoped to several hosts are unreadable, and pages scroll sideways on mobile or narrow embeds.

Suggested fix: Truncate or line-clamp the secondary line, or show hosts as chips or a count with a tooltip. Make the header flex-col sm:flex-row (or flex-wrap).

Files: web/src/routes/(dashboard)/credentials/+page.svelte, web/src/routes/(dashboard)/schedules/+page.svelte, web/src/routes/(dashboard)/datastores/+page.svelte

---
### Executions of deleted workflows are listed by raw workflow id with no "deleted" marker [find:ui-ops-surfaces] (low/ui) · area: executions list · confidence: high

When a workflow is deleted, its executions stay, and the Workflow column falls back to the raw workflow id. The name is still available from the pinned revision.

Evidence: exec_01a0b8d3-f2c7-… belongs to wf_01a0b8d3-ee83-…, which returns 404. The list shows the id (`workflowNames.get(item.workflowId) ?? item.workflowId`; example row in 01-executions-list.png). GET /workflows/{id}/versions/{versionId} still returns 200.

n8n behavior: n8n removes executions together with their workflow.

Impact: Unreadable history after workflows are deleted.

Suggested fix: Include workflowName from the pinned revision in ExecutionSummary and label it "(deleted)", or delete or offer to delete executions along with their workflow.

Files: web/src/routes/(dashboard)/executions/+page.svelte, internal/api/handlers/executions.go

---
### Datastore grid: rows cannot be edited, and there is no filter, sort or search [find:web-frontend-code] (low/parity-gap) · area: datastores UI · confidence: high

The datastore detail page uses only list, insert and delete for rows. updateDatastoreRows and upsertDatastoreRow have no call sites, and listDatastoreRows is called with only limit and cursor. Fixing one wrong cell means deleting the row and re-inserting it, which gives it a new id.

Evidence: web/src/routes/(dashboard)/datastores/[id]/+page.svelte imports only insertDatastoreRow, deleteDatastoreRows and listDatastoreRows (lines 17-21). The API has PUT /datastores/{id}/rows and POST .../rows/upsert.

n8n behavior: n8n Data tables support inline cell editing, sorting and filtering.

Impact: Operators cannot correct data that workflows read, such as lookups and dedup keys, without breaking row identity.

Suggested fix: Add inline editable cells that call updateDatastoreRows filtered by id, and column sort and filter wired to the list API's filter.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/datastores/[id]/+page.svelte

## Acceptance criteria

- [ ] Credential form ignores type defaults (Telegram baseUrl), accepts invalid values, and has no Test button altho
- [ ] Schedules page: schedules for inactive workflows show "Active · Next …" then pause silently; trigger-owned sch
- [ ] Datastore grid supports reading and adding only: no cell editing, search, filter, sort or total count, althoug
- [ ] No framing policy on any page: /embed is not limited to the configured embed origins, and the dashboard and /a
- [ ] Credentials, Schedules and Datastores lists: fixed-height rows overlap long content, and the page header overf
- [ ] Executions of deleted workflows are listed by raw workflow id with no "deleted" marker
- [ ] Datastore grid: rows cannot be edited, and there is no filter, sort or search
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress (WebFormsOps2 2026-09-19)

- Commit df6c87c: datastore grid — server-side text search (ilike %pattern% OR across text columns via list params), client-side column sort (sortable header buttons with aria-labels), inline type-aware cell editing via updateDatastoreRows (Enter/blur commit, Escape cancel, system columns read-only), per-page row count + cell error surface. svelte-check clean; columns.test.ts 13 pass.
- Commit 34ee7d4: schedules — trigger-owned rows (nodeId) read-only with "open in editor" link, inactive-workflow warning ("won't fire until activated"), create-refused for inactive workflows, delete confirm; credentials/datastores/schedules headers flex-col on phones (fixes 390px overflow). Credential rows already min-h-11 + truncate + title (prior agent).
- Remaining: framing headers (SecurityDx owns), credential usage count (needs API), deleted-workflow marker on executions list, schedules "why paused" record. Credential form defaults+test done by prior agent (f4322ac).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (6):
  - `f4d28d0c` — FEAT-0895qc FEAT-56nep4 BUG-6bqh51 BUG-esb9sh: record testing state — slices landed with scoped proof
  - `396d6ca8` — BUG-esb9sh BUG-6bqh51: record fix evidence — ops grids, execution detail
  - `34ee7d42` — BUG-esb9sh: schedules trigger-owned guards + responsive headers — ops lists
  - `df6c87ce` — BUG-esb9sh: datastore grid search/filter/sort/inline cell editing — ops tables
  - `f4322ac7` — BUG-npfz43 BUG-mewhrd: credential secrets empty+keep-marker, delete confirm, defaults+test — ops forms
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
 .pine/tickets/BUG-esb9sh.md                        |  138 ++
 .pine/tickets/BUG-f9frth.md                        |  410 +++
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
 434 files changed, 62875 insertions(+), 4725 deletions(-)
```
