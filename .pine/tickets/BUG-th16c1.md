---
id: BUG-th16c1
title: Dashboard listings hide rows past the first cursor page (workflows/credentials/datastores/schedules/api-keys)
status: testing
priority: medium
created: "2026-09-20T02:32:33Z"
updated: "2026-09-20T03:12:18Z"
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
