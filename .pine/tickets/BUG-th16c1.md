---
id: BUG-th16c1
title: Dashboard listings hide rows past the first cursor page (workflows/credentials/datastores/schedules/api-keys)
status: done
priority: medium
created: "2026-09-20T02:32:33Z"
updated: "2026-09-20T03:17:21Z"
---

# Description

Found by ReviewWeb (b4f2147..HEAD), P1. The listing endpoints were paged
server-side (BUG-fv5fer / BUG-8dmp5y): every one of them answers one page —
default 100, maximum 500, cursor in the `X-Next-Cursor` response header and
passed back as `?cursor=` — while every dashboard page read a single response
as if it were the whole workspace. The same defect reached six surfaces:
workflows, credentials, datastores, schedules, API keys, and both credential
pickers plus the workflow-name/filter data on the executions page.

# Steps to Reproduce

Seeded one instance (SQLite, `KILASFLOW_DATABASE_DSN=/tmp/kflow-p1/kilasflow.db`)
with 618 workflows, 105 credentials, 105 schedules, 105 API keys and 100
datastores (the datastore cap), then opened the dashboard against it:

- `/app/workflows` heading read "100 in this workspace" and the list paged over
  100 rows; `p1-flow-000`, the first workflow created, was unreachable from the
  UI at any page.
- `/credentials`, `/schedules` and `/settings` each listed 100 of 105 rows.
- `/executions` labelled the execution of `p1-run-target` — a workflow sitting
  at position 302 of the newest-first server listing — "deleted", because the
  name map only ever held the first 100 workflows. Its filter offered 101
  workflows of 618.

# Expected

Every row of every listing is on screen, the counts are the tenant's counts, and
a workflow is called deleted only when the complete list does not contain it.

# Actual

Rows past the first page were unreachable, the workflows heading under-counted
the tenant, and executions of those workflows were labelled deleted.

# Fix

Commit `4a49d70` (`BUG-th16c1: the dashboard lists drain every cursor page
instead of showing the first — web`).

`web/src/lib/dashboard/cursor-page.ts` gained the two pieces the pages were
missing, both unit-tested in `cursor-page.test.ts`:

- `headerCursor(headers)` — the `X-Next-Cursor` read, normalized to `''` when
  the last page omits it.
- `drainPages(fetchPage)` — follows the cursor to the end and returns the rows in
  page order. A rejection ends the drain and propagates: half a list is not a
  list, and a caller that rendered one would print a wrong count rather than an
  error. A cursor that does not advance ends the drain with an error too, so a
  server echoing a cursor back cannot be re-requested forever.
- `DRAIN_PAGE_LIMIT = 500` — the largest page every listing endpoint serves, so
  a 618-row tenant costs two requests instead of seven.

Each surface now drives its own list state (`loading`/`failure`) through
`drainPages` and feeds the same booleans to `ListStates`: the pages keep their
rows while a reload runs (only the first load shows the skeleton, as the query
object behaved), and a first-load failure shows the error card rather than a
partial list. `web/src/routes/(dashboard)/executions/+page.svelte` drains the
workflow listing it reads names, filter options and the deleted badge from, and
`workflows/[id]/+page.svelte` and `lib/embed/embed-editor.svelte` drain the
credential listing their pickers offer (a failure there still leaves the picker
empty rather than blocking the editor).

# Evidence

Live, against the seeded instance above:

- `/app/workflows`: "316 in this workspace" with 316 rows on the server (4
  pages), page 13 of 13 holding `p1-flow-000` — a row from the server's last
  page. After the fix, deleting a row through the UI reloads to 617.
- `/credentials` 105 rows (server: 105 over 2 pages), `/schedules` 105 (2
  pages), `/settings` 105 API keys (2 pages), `/datastores` 100 (the cap, 1
  page).
- `/executions`: the filter offers 619 options (618 workflows + "All
  workflows"), the row for `p1-run-target` — server position 302 — shows its
  name with no badge, and an execution of a genuinely deleted workflow still
  shows "deleted".
- `cd web && npx vitest run` 503 passed (44 files), `npx svelte-check
  --tsconfig ./tsconfig.json` 1517 files, 0 errors, 0 warnings.

# Acceptance Criteria

- [x] No row of the five listings is hidden behind the first server page.
- [x] The workflows heading and "page X of Y" count the whole tenant.
- [x] A workflow is labelled deleted only when the complete listing lacks it.
- [x] A mid-drain failure shows a failure instead of a partial list with a wrong
      count (unit-tested, `cursor-page.test.ts`).
- [x] `409`/`baseVersionId`, the unsaved guard, the banner refetch, the
      diagnostic badge and the selection loop (`sameSelection`) are untouched.

# Related Files

- web/src/lib/dashboard/cursor-page.ts, cursor-page.test.ts
- web/src/routes/(dashboard)/app/workflows/+page.svelte
- web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte
- web/src/routes/(dashboard)/credentials/+page.svelte
- web/src/routes/(dashboard)/datastores/+page.svelte
- web/src/routes/(dashboard)/executions/+page.svelte
- web/src/routes/(dashboard)/schedules/+page.svelte
- web/src/routes/(dashboard)/settings/+page.svelte
- web/src/lib/embed/embed-editor.svelte

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `f5d0f320` (last commit at or before ticket created 2026-09-20)
- Commits (4):
  - `aabb522d` — chore(pine): commit the outstanding ticket notes
  - `b8bc90ba` — chore(pine): record the web review's P1 and keyboard/doc fixes on their tickets
  - `4a49d70e` — BUG-th16c1: the dashboard lists drain every cursor page instead of showing the first — web
  - `fbfc7692` — chore(pine): reopen 6jvcs5, cq4yk3, kzkvv6, 6as5y7 with the importer review's findings
- Files changed (base → working tree):

```
 .pine/tickets/BUG-4053h6.md                        | 350 ++++++++------
 .pine/tickets/BUG-6as5y7.md                        | 281 ++++++-----
 .pine/tickets/BUG-6jvcs5.md                        | 279 ++++++-----
 .pine/tickets/BUG-8dmp5y.md                        | 284 ++++++-----
 .pine/tickets/BUG-8sb0jw.md                        | 276 ++++++-----
 .pine/tickets/BUG-9853ay.md                        | 284 +++++------
 .pine/tickets/BUG-aede06.md                        | 271 ++++++-----
 .pine/tickets/BUG-cq4yk3.md                        | 271 ++++++-----
 .pine/tickets/BUG-fv5fer.md                        | 276 ++++++-----
 .pine/tickets/BUG-kzkvv6.md                        | 262 +++++-----
 .pine/tickets/BUG-qmgz2f.md                        | 252 +++++-----
 .pine/tickets/BUG-tcqkad.md                        | 243 +++++-----
 .pine/tickets/BUG-th16c1.md                        | 110 +++++
 .pine/tickets/BUG-y57cz4.md                        |  36 +-
 .pine/tickets/BUG-ysvmaa.md                        |  42 +-
 .pine/tickets/FEAT-qdedm0.md                       |  15 +
 docs/src/content/docs/start/install.md             |   2 +-
 internal/ai/agent.go                               |   3 +
 internal/ai/ai.go                                  |  16 +-
 internal/ai/openai.go                              |   8 +-
 internal/api/cors_test.go                          |  34 ++
 internal/api/credentials_test.go                   |  98 ++++
 internal/api/datastores_test.go                    |  41 ++
 internal/api/embed_confinement_test.go             |  68 +++
 internal/api/embed_test.go                         |  27 ++
 internal/api/events_test.go                        |  89 ++++
 internal/api/handlers/credentials.go               |  28 +-
 internal/api/handlers/datastores.go                |  44 +-
 internal/api/handlers/datastores_csv.go            |   6 +-
 internal/api/handlers/executions.go                |  22 +-
 internal/api/handlers/interop.go                   |  12 +-
 internal/api/handlers/nodes.go                     |  44 +-
 internal/api/handlers/problem.go                   |  81 ++++
 internal/api/handlers/schedules.go                 |  17 +-
 internal/api/middleware/cors.go                    |  12 +-
 internal/api/middleware/cors_test.go               |  50 +-
 internal/api/node_types_test.go                    |  85 +++-
 internal/api/routes.go                             |  29 +-
 internal/api/server.go                             |  18 +-
 internal/config/boot_strictness_test.go            |  66 +++
 internal/config/config.go                          |  24 +-
 internal/embed/embed.go                            |   9 +
 internal/embed/embed_test.go                       |  14 +
 internal/engine/runner.go                          | 100 +++-
 internal/engine/service.go                         |   2 +
 internal/engine/wait_service.go                    |   2 +-
 internal/engine/wait_service_test.go               | 147 +++++-
 internal/execution/records.go                      |  11 +
 internal/expression/doc.go                         |  13 +
 internal/expression/evaluator.go                   | 227 +++++++--
 internal/expression/expression.go                  | 165 ++++++-
 internal/expression/globals.go                     |  45 +-
 internal/expression/methods.go                     |  40 +-
 internal/expression/parity_test.go                 | 536 +++++++++++++++++++++
 internal/expression/roots.go                       |  26 +
 internal/interop/n8n/importer_tail_test.go         | 323 ++++++++++++-
 internal/interop/n8n/n8n.go                        |  31 ++
 internal/interop/n8n/parameters.go                 | 262 ++++++++--
 internal/repository/executions.go                  |  12 +
 internal/repository/models.go                      |  19 +-
 internal/repository/subworkflow_activation_test.go |  81 ++++
 internal/webhook/webhook.go                        | 105 +++-
 internal/webhook/webhook_test.go                   | 111 +++++
 .../postgres/000013_node_run_response.down.sql     |   9 +
 .../postgres/000013_node_run_response.up.sql       |  28 ++
 .../sqlite/000013_node_run_response.down.sql       |   9 +
 migrations/sqlite/000013_node_run_response.up.sql  |  23 +
 nodes/ai.go                                        |  51 +-
 nodes/ai_test.go                                   | 141 ++++++
 nodes/embedscope.go                                |  59 ++-
 nodes/embedscope_test.go                           |  46 ++
 nodes/http.go                                      |  54 ++-
 nodes/http_test.go                                 |  57 ++-
 nodes/subworkflow.go                               |  85 +++-
 nodes/subworkflow_calls_test.go                    |  34 ++
 nodes/webhook.go                                   |   7 +-
 .../workflow-editor/property-field.svelte          |  91 +++-
 .../workflow-editor/workflow-editor.svelte         |  13 +-
 web/src/lib/dashboard/cursor-page.test.ts          |  85 +++-
 web/src/lib/dashboard/cursor-page.ts               |  57 +++
 web/src/lib/embed/embed-editor.svelte              |  27 +-
 .../lib/workflow-editor/expression-assist.test.ts  |  55 ++-
 web/src/lib/workflow-editor/expression-assist.ts   |  50 ++
 web/src/lib/workflow-editor/shortcuts.test.ts      |  34 +-
 web/src/lib/workflow-editor/shortcuts.ts           |  24 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |  81 +++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  26 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  60 ++-
 web/src/routes/(dashboard)/datastores/+page.svelte |  59 ++-
 web/src/routes/(dashboard)/executions/+page.svelte |  53 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |  93 ++--
 web/src/routes/(dashboard)/settings/+page.svelte   |  56 ++-
 92 files changed, 6162 insertions(+), 2042 deletions(-)
```
