---
id: BUG-esb9sh
title: 'Ops credentials/datastore/schedules UI: forms, validation, test, grid editing, framing, markers'
status: testing
priority: medium
labels:
    - ui
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T14:05:05Z"
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
