---
id: FEAT-qdedm0
title: Dashboard lists fetch every row; adopt limit/cursor from the paged list endpoints
status: doing
priority: medium
created: "2026-09-20T00:51:08Z"
updated: "2026-09-20T07:42:30Z"
---

# Description

# Acceptance Criteria
- [x] The paging policy is decided and recorded with its evidence: the executions list reads lazily by cursor ("Load more") because it grows with traffic (execution.retention defaults to 0 = keep everything; the API serves 25 rows a page by default and 100 at most; the keyset cursor on (started_at, id) is stable under inserts because started_at is written once at queue time), while the workflows, credentials, datastores, schedules and API-keys listings and both credential pickers keep draining every page because they are bounded by authoring rather than traffic and each page needs the whole set (search, filter, sort, counts, name lookup, duplicate-name check, pickers). The decision is written in the ticket's Implementation notes and in the doc comment of web/src/lib/dashboard/cursor-page.ts, with the trigger for revisiting it. — Proven in Stage 3: the policy is written at the top of web/src/lib/dashboard/cursor-page.ts as "Two ways to read a paged listing": DRAIN (drainPages, DRAIN_PAGE_LIMIT = 500) naming workflows, credentials, datastores, schedules, API keys, the workflow-name maps on the executions and schedules pages and both credential pickers, with the per-tenant datastore cap; LOAD MORE (readPage/appendPage/appendPageIfCurrent/canLoadMore/mergeHead, EXECUTIONS_PAGE_LIMIT = 50) naming executions plus the datastore rows grid and the version panel; and the ~5,000-row revisit trigger with the workflow-name-drain ceiling. The Decision section below carries the file:line evidence.
- [x] The executions list requests an explicit page size (EXECUTIONS_PAGE_LIMIT = 50, within the API maximum of 100) on its first load, on every "Load more" and on every auto-refresh, and never drains: each Load more appends exactly the next page in order with no row duplicated or skipped, including when an auto-refresh lands while a Load more is in flight, and the Load more button disappears when the server sends no cursor.
- [x] The executions list says what it holds: a visible line reading "Showing the newest N executions. More are available." while pages remain and "Showing all N executions." once the list is complete (with the singular, and the "matching these filters" variants), and it never states a total the server did not give. The strings live in one function (executionListSummary) with unit tests.
- [x] The executions status and workflow filters stay server-side, so they apply to the whole history, and the page offers no client-side search or sort over the loaded rows; the "Find workflow" box only narrows the workflow filter's options, which are read to the end.
- [x] Auto-refresh keeps working when more pages exist: a run that starts while the list is open appears at the top, statuses of rows in the newest page update, the older rows already loaded and the position of Load more are kept while fewer than one page of new runs (EXECUTIONS_PAGE_LIMIT) arrives between two polls — a whole page or more falls back to the newest page alone, the boundary the Known limits record —, and a list that was loaded to its end is not cut back to the first page. Polling pauses while the tab is hidden and resumes when it becomes visible again, and does not run while a load or a first-load failure is showing. — Proven in Review round 1: the sixth e2e case ("auto-refresh keeps running after a poll that finds nothing new", e2e/tests/dashboard-lists.spec.ts) lets two real poll intervals elapse with nothing new before creating a run; it is red when `void pollTurn;` is removed from the poll effect (`expect(locator).toHaveCount` `Expected: 4 / Received: 3` after 8 s) and green with it restored, and the boundary above is the gap fallback the Known limits record.
- [x] The lists that drain still show the whole tenant (drainPages unchanged, its tests green), and their counts are honest: the workflows heading shows the workspace total, not the filtered count, and the list header reads "N of M workflows" while a search or filter is active; the stale comments that claim the API has no server paging (workflow-list.ts) and that executions is the only list that pages a cursor (executions page) are corrected. — Proven in Stage 3: e2e test 5 ("the workflows heading counts the workspace and the list header counts the search", e2e/tests/dashboard-lists.spec.ts) was red on the unwired tree — after the 'Alpha' search `getByText('3 in this workspace', { exact: true })` was `element(s) not found` because the heading printed `filtered.length` — and green after the three edits (heading `allRows.length`; list header and bottom pager `workflowCountLabel(filtered.length, allRows.length)`): with 3 workflows the heading reads "3 in this workspace" before and after the search, and the list header reads "1 of 3 workflows · page 1 of 1". Both stale comments are gone: `git grep -n "every summary in one response\|only one that pages a cursor" web/src` prints nothing. `drainPages` is unchanged and `pnpm test` (44 files / 556 tests) is green.
- [x] No e2e spec opened the executions list, so none needed updating, and a new e2e spec covers Load more, the count line and auto-refresh over a paged history.
- [x] The web gates pass and the conventions hold: `pnpm check` and `pnpm test` pass from web/, `go test ./internal/guardrails/...` passes (also after the commits, since it scans tracked files only), no dependency was added (so no licence question), user-visible strings introduced are plain English kept in pure functions (executionListSummary, workflowCountLabel), and only existing dark-mode-first tokens are used (no literal colours). — Proven in Stage 1: `pnpm check` = 1517 files, 0 errors, 0 warnings; `pnpm test` = 44 files / 556 tests passed; `go test ./internal/guardrails/...` = ok (before and after the commit); `git status --short` shows only the ticket and the six `web/src/lib/dashboard/*` files, so package.json and the lockfile are untouched; the only new user-visible strings are the ones pinned by the `executionListSummary` and `workflowCountLabel` unit tests; Stage 1 changed no markup, so no colour or token was added. Gates are re-run in Stages 2-3 for the page and e2e changes.

# Implementation Plan

# Notes

# Related Files

# Attachments

Links: FEAT-0895qc (list UX), BUG-fv5fer (endpoints). Created from DXOps2's note: the four list endpoints advertise limit/cursor and no dashboard page uses them; passing undefined preserves behaviour but the half-wired surface should be adopted or documented before FEAT-0895qc closes.

## Note (2026-09-20)

The premise of this ticket — no dashboard page uses the `limit`/`cursor` the
list endpoints advertise — is closed by BUG-th16c1 (`4a49d70`): the six surfaces
that read those listings now drain every page through
`web/src/lib/dashboard/cursor-page.ts` (`drainPages`, `headerCursor`,
`DRAIN_PAGE_LIMIT = 500`, unit-tested) and the five listings plus the executions
workflow map and both credential pickers show the whole tenant again.

What is deliberately **not** done, and is the remaining product decision here: a
"Load more" list that fetches pages lazily instead of draining up-front. The
drain keeps the current behaviour — search, sort and the "N in this workspace"
count work over the complete list — at the cost of one request per 500 rows.
Left open for that call rather than closed.

## Implementation notes

### Decision

Two ways to read a paged listing, and which surface takes which. Evidence
verified against the tree in this worktree (`aa307d4`).

1. Executions grow with traffic, so the executions list reads lazily by cursor
   ("Load more"). `execution.retention` defaults to 0 = keep every run
   (internal/config/config.go, `Execution.Retention`, "Default: 0 (keep
   everything)"; config.example.yaml `execution.retention: 0`), so the history
   has no upper bound. The listing is keyset on `(started_at, id)`
   (internal/repository/executions.go:406, ordered `started_at DESC, id DESC`),
   and `started_at` is written once at queue time (executions.go:171 and
   :291-292) and never updated anywhere in internal/ — no update path writes the
   `started_at` column — so a run created while a user pages cannot shift rows
   across pages and a row never changes position. The endpoint serves 25 rows a
   page by default and 100 at most (`DefaultExecutionPageSize = 25`,
   `MaxExecutionPageSize = 100`, internal/repository/executions.go:37-38;
   `maximum:"100"` at internal/api/handlers/executions.go:157). Lazy "Load more"
   has existed on this page since 147f782; what this ticket finishes is making it
   correct and honest (defects (a)-(c) below).
2. The other lists are bounded by authoring, not traffic, so they keep draining
   every page. Workflows, credentials, datastores, schedules and API keys are
   created by people, not by traffic; datastores are additionally capped per
   tenant by `datastore.max_datastores_per_tenant` (default 100,
   internal/config/config.go:627-628). Their endpoints hold up to 500 rows a page
   (`maximum:"500"`: internal/api/handlers/workflows.go:255, credentials.go:122,
   datastores.go:79, schedules.go:71, auth.go:175), so 618 workflows cost two
   requests. Each of those surfaces computes over the whole set: workflows
   search, filter, sort and count over every row and build the duplicate-name
   `taken` set from all of them; the executions and schedules pages resolve
   workflow names and the "deleted" badge from the complete workflow list; and
   both credential pickers (embed-editor.svelte, workflows/[id]/+page.svelte)
   list every credential. Half a list would print a wrong count and a wrong
   "deleted" badge, which is worse than one extra request.
3. The executions list asks for an explicit 50 rows a page — twice the server
   default of 25, half the API maximum of 100 — and never relies on the default.
   The value lives in one place (`EXECUTIONS_PAGE_LIMIT` in
   web/src/lib/dashboard/execution-list.ts) so the first load, every "Load more"
   and every auto-refresh agree.
4. Executions has no client-side search or sort, and none is added. Status and
   workflow are server-side filters over the whole history, which is the honest
   behaviour; a free-text filter over only the 50 loaded rows would silently hide
   matching runs further down the history, and a client sort over a partial list
   would order the fragment. The count line therefore states only what is loaded
   ("Showing the newest 50 executions. More are available.") and never a total
   the server did not send.
5. Trigger for revisiting: a drained list that realistically passes ~5,000 rows
   moves to server-side q/limit rather than a bigger drain, and the executions
   page still drains every workflow only to resolve names (2 requests at ~600
   workflows), which a server-side ids=/q= lookup on GET /workflows would remove.

### Defects this ticket fixes on the executions page

Found by reading web/src/routes/(dashboard)/executions/+page.svelte; (a)-(c) are
confirmed by the Stage 2 e2e red run, not by reading alone.

- (a) The poll effect returned early on `if (page.nextCursor) return;`, so
  auto-refresh never ran for any tenant with more than 25 runs.
- (b) A poll over a list loaded to its end replaced the whole list with the
  first page (`page = { items: head.items, nextCursor: page.nextCursor }`), so
  the list silently shrank and "Load more" never came back.
- (c) The effect read the non-reactive `document.hidden` and `refreshHead`
  returned without changing any state, so nothing re-armed the timer: polling
  stopped for good after the tab had been hidden once.
- (e) A race the merge-based poll makes reachable: a poll that resolves with the
  gap fallback while a "Load more" request is in flight, whose page then appends
  onto the replaced list, leaves a hole. Closed by `appendPageIfCurrent`.

### Stage 1 — the paging state machine as pure, unit-tested functions

**What changed**

- `web/src/lib/dashboard/cursor-page.ts`: `mergeHead(loaded, head, keyOf)` folds a
  fresh first page into a list that has paged on — the head takes over the rows it
  covers (which refreshes their status), the older rows already loaded and the
  cursor "Load more" resumes from survive, and a head that no loaded row joins
  returns the head alone rather than splicing across a gap.
  `appendPageIfCurrent(loaded, askedWith, response)` appends only while the list
  still waits on the cursor the request was made with (defect (e)). Both are pure
  and never mutate their arguments.
- `web/src/lib/dashboard/execution-list.ts`: `EXECUTIONS_PAGE_LIMIT = 50`,
  `HEAD_POLL_INTERVAL_MS = 5000`, `buildListExecutionsParams(filters, cursor)` (an
  explicit limit, the status as the array the API takes, unset filters and cursor
  omitted), `HeadPollSignals` + `shouldPollHead` (no cursor or loaded-row signal
  at all, so a paged-on list cannot switch the poll off), and
  `executionListSummary` (all of the count-line copy in one function).
- `web/src/lib/dashboard/workflow-list.ts`: `workflowCountLabel(shown, total)`, and
  the stale header comment ("every summary in one response (no server paging)") is
  corrected to say the API pages server-side and the page drains it.
- 42 new unit tests across `cursor-page.test.ts` (19: 12 `mergeHead`, 3
  `appendPageIfCurrent`, 4 driving a fake keyset API through a growing history),
  `execution-list.test.ts` (20) and `workflow-list.test.ts` (3).

**Verification**

- `cd web && pnpm install --frozen-lockfile` — installed; lockfile untouched.
- `cd web && pnpm vitest run src/lib/dashboard` — RED first, before the
  implementation: `Test Files 3 failed | 3 passed (6)`, `Tests 42 failed | 74
  passed (116)`, every failure a `TypeError: mergeHead/appendPageIfCurrent/
  buildListExecutionsParams/shouldPollHead/executionListSummary/workflowCountLabel
  is not a function` (the one `EXECUTIONS_PAGE_LIMIT` test failed as
  `undefined > 0`). No syntax or import-path errors. GREEN after: 116 passed.
- `cd web && pnpm check` — `1517 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS`.
- `cd web && pnpm test` — `Test Files 44 passed (44)`, `Tests 556 passed (556)`.
- `go test ./internal/guardrails/...` — `ok github.com/kilaslab/kilas-flow/internal/guardrails`
  (run before the commit and again after it; it scans `git ls-files`, and every file
  this stage touched is already tracked).
- `git grep -n "every summary in one response" web/src` — prints nothing (it matched
  `workflow-list.ts:5` before this stage).
- `git status --short` — only this ticket file and the six `web/src/lib/dashboard/*`
  files. No dependency added (package.json and the lockfile are untouched), so
  there is no licence question.
- `git log main..HEAD --format=%B | grep -ciE 'co-authored-by|claude-session|generated with'`
  — `0`.

**Still open after stage 1** (superseded by Stage 2 below except criterion 1's `cursor-page.ts` doc comment and criterion 6's workflows half)

Each remaining criterion has a page or e2e half that lands in Stages 2-3, which is
why none of them is ticked yet. Criterion 1 is half done: the Decision section and
these notes are in place, and the policy block at the top of `cursor-page.ts` is
Stage 3. Criterion 2 has its request builder, unit-tested, but the page still
builds its own query until Stage 2 routes all three `listExecutions` call sites
through `buildListExecutionsParams`. Criterion 3 has `executionListSummary` with
its exact-string tests; the visible line is rendered in Stage 2. Criterion 4 is a
property of the Stage 2 wiring, since nothing on the page changed here. Criterion 5
has `mergeHead` and `shouldPollHead` unit-tested; the poll effect, the reactive
hidden state and the four e2e tests are Stage 2. Criterion 6 has
`workflowCountLabel` and the corrected `workflow-list.ts` comment in place; the
workflows heading and list header are Stage 3 and the second stale comment (on the
executions page) is Stage 2. Criterion 7 is the new e2e spec in Stage 2. Criterion
8 is proven here and ticked above.

### Stage 2 — the executions page wired: e2e red first, then the explicit page size, the count line and a poll that composes with paging

**Test first**

`e2e/tests/dashboard-lists.spec.ts` is new and holds four tests: Load more plus
the count line; auto-refresh with a cursor present; auto-refresh over a list
loaded to its end; and hidden-tab pause/resume. `PAGE = 50` mirrors
`EXECUTIONS_PAGE_LIMIT` in `web/src/lib/dashboard/execution-list.ts`, so the
seeded history and every expected string are derived from one number.

`cd e2e && pnpm exec playwright test tests/dashboard-lists.spec.ts --reporter=line --retries=0`
against the unwired page — **4 failed**, each for the reason it exists to prove:

- test 1 — `expect(locator).toBeVisible() failed … element(s) not found` for
  `Showing the newest 50 executions. More are available.` (the line did not exist).
- test 2 — `Expected: 26 / Received: 25`: the count never moved. With a cursor
  present the poll returned early (defect a), and the 25 rows are the server
  default the page used to take without asking.
- test 3 — `Expected: 61 / Received: 25`, having first resolved to 60 rows and
  then to 25: the poll replaced the whole list with the first page (defect b).
- test 4 — `Expected: 4 / Received: 3` at the resume step: polling never came
  back after the tab was hidden (defect c).

**What changed** — `web/src/routes/(dashboard)/executions/+page.svelte` only

- All three `listExecutions` call sites go through `buildListExecutionsParams`,
  so the first load, every Load more and every poll carry `limit=50` (the three
  calls are the only ones in the file).
- `loadMore` captures the cursor it asked with and appends through
  `appendPageIfCurrent`, so a page arriving after a poll moved the cursor is
  dropped rather than spliced on (defect e).
- `refreshHead` guards with `shouldPollHead(pollSignals())` (one `pollSignals()`
  used by the effect and by the timer path), reads `page` at the moment the
  response resolves and folds the head in with `mergeHead(page, readPage(...),
  (row) => row.id)`; its `finally` bumps the new `pollTurn` so the chain survives
  a poll that finds nothing new.
- New reactive `tabHidden`, seeded in `onMount` and updated by
  `<svelte:document onvisibilitychange=…>`, is what lets the poll effect re-arm
  when the tab comes back (defect c). The effect no longer reads
  `document.hidden` directly and no longer returns early on `page.nextCursor`
  (defect a), and it merges instead of replacing (defect b).
- The count line renders `executionListSummary({ count, hasMore, filtered })` in
  a plain `<p class="mb-2 text-xs text-muted-foreground">` above the table — not
  `role=status`, so auto-refresh does not announce every new run — using only an
  existing token.
- The stale comment above `const guard = new RequestGuard();` is replaced with
  the lazy-vs-drain policy for this page; no client-side search or sort was added
  (`page.items` is only counted and iterated, never filtered or sorted).

**Verification**

- Green e2e: `4 passed (43.6s)`; `--repeat-each=3 --retries=0` → `12 passed (1.2m)`.
- `cd web && pnpm check` — `1517 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS`.
- `cd web && pnpm test` — `Test Files 44 passed (44)`, `Tests 556 passed (556)`.
- Phone width (390×844, the real binary, 60 seeded runs, Playwright):
  `documentScrollWidth 390 = innerWidth 390`, the only scroller on the page is
  the table's own `div.overflow-x-auto` (`clientWidth 356`, `scrollWidth 882`),
  and the summary renders as one 358 px line ("Showing the newest 50 executions.
  More are available."). It does not wrap at 390 px because it fits there; what
  matters is that the page does not scroll sideways.

### Stage 3 — honest counts on the drained lists, the paging policy in code, changelog, ticket close-out

**Test first**

`e2e/tests/dashboard-lists.spec.ts` gains a fifth test, "the workflows heading
counts the workspace and the list header counts the search": it seeds three
workflows, expects the heading `3 in this workspace`, types `Alpha` into the
page's `Search` searchbox, then expects the heading to still read
`3 in this workspace` and the list header to read `1 of 3 workflows · page 1 of 1`.

`cd e2e && pnpm exec playwright test tests/dashboard-lists.spec.ts --reporter=line --retries=0 -g "workflows heading counts"`
— **RED**, `1 failed`: `expect(locator).toBeVisible() failed … element(s) not
found` for `getByText('3 in this workspace', { exact: true })` at the
post-search assertion, because the heading printed `filtered.length`. The
pre-search assertion passed, so the searchbox and heading locators are right and
the failure is the count, not the search.

**What changed**

- `web/src/routes/(dashboard)/app/workflows/+page.svelte` — three edits plus the
  `workflowCountLabel` import: the header paragraph prints `allRows.length`
  ("N in this workspace"), and the list header and the bottom pager print
  `workflowCountLabel(filtered.length, allRows.length)` ("N of M workflows" while
  a search or filter narrows the list). Nothing else on that page changed.
- `web/src/lib/dashboard/cursor-page.ts` — a module doc block, "Two ways to read
  a paged listing": DRAIN versus LOAD MORE, which surface takes which and why,
  the ~5,000-row revisit trigger and the workflow-name-drain ceiling.
- `e2e/tests/dashboard-lists.spec.ts` — the fifth test; the file header now says
  it covers the dashboard's list surfaces (executions lazily, workflows by
  draining) rather than the executions page alone.
- `CHANGELOG.md` — `### Changed` and `### Fixed` under `[Unreleased]`, in
  Keep-a-Changelog order between `### Added` and `### Security`.

**Verification**

- `cd e2e && pnpm exec playwright test tests/dashboard-lists.spec.ts --reporter=line --retries=0`
  — **GREEN**, `5 passed (1.9m)`; `--repeat-each=3 --retries=0` → `15 passed (2.7m)`.
- `cd web && pnpm check` — `1517 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS`.
- `cd web && pnpm test` — `Test Files 44 passed (44)`, `Tests 556 passed (556)`.
- `go test ./internal/guardrails/...` — `ok github.com/kilaslab/kilas-flow/internal/guardrails` (run before the commit and again after it).
- No e2e spec opened the executions list before this ticket:
  `grep -rnE "goto\(" e2e/tests --exclude=dashboard-lists.spec.ts | grep -i execution`
  prints nothing and
  `grep -rn "Load more\|Auto-refresh\|open-executions" e2e/tests --exclude=dashboard-lists.spec.ts`
  prints nothing; the other specs that mention `/executions`
  (live-backend-queue, live-backend-api, library-import, n8n-compare,
  live-backend-webhook, ai-agent-ollama) call the REST API only.
- `git grep -n "every summary in one response\|only one that pages a cursor" web/src`
  — prints nothing (it matched `workflow-list.ts:5` and the executions page before
  Stages 1-2).
- `git log main..HEAD --format=%B | grep -ciE 'co-authored-by|claude-session|generated with'`
  — `0`.

**Not applicable** — no Go, API, SDK, config, migration, dialect or tenancy change
was made: no `.go`, `internal/`, `cmd/`, `sdk/`, config, `migrations/` or
generated-artifact file is touched (`git diff --name-only main...HEAD` is only
`web/src/`, `e2e/tests/`, `CHANGELOG.md` and this ticket), so `gofmt`, `go vet`,
`go build`, `go test -race`, the `generate-*`/`-check` and `sdk-*` targets, the
configuration-reference check and the SQLite/PostgreSQL migration gates do not
apply. No dependency was added, so there is no licence question. No
`docs/src/content/docs` page was edited (the dashboard has no docs page), and this
ticket changes none of the sentences BUG-vzzkg3 owns.

### Review round 1 — the poll chain gets a test, and three texts tell the truth

The correctness lens found nothing (a 3,000-trial randomised property probe over
`mergeHead`); these fixes close the acceptance lens's medium test gap and its low
boundary note, and the conventions lens's two stale statements. Findings:
`/tmp/kf-wave1/reviews/FEAT-qdedm0.confirmed.json`.

**Test first (finding 1, medium — the untested poll chain).** The poll `$effect`
re-arms after a poll that finds nothing new only because `refreshHead`'s `finally`
writes `pollTurn`, which the effect reads; nothing drove a second idle poll, so
deleting that read killed auto-refresh while `pnpm check`, `pnpm test` and all
five e2e cases stayed green. `e2e/tests/dashboard-lists.spec.ts` gains a sixth
test, "auto-refresh keeps running after a poll that finds nothing new": seed 3
runs, open `/executions`, expect 3 rows, let two real 5 s poll intervals elapse
with nothing new (`waitForTimeout(12_000)`, still 3 rows), then create and run a
workflow and expect a fourth row within 8 s. Load-bearing proof: with
`void pollTurn;` deleted from the effect the case fails `1 failed (59.7s)` —
`expect(locator).toHaveCount` `Expected: 4 / Received: 3` (20 × locator resolved
to 3 elements) — and passes again `1 passed (59.9s)` once the read is restored.

**The three corrections (findings 2-4, low).**

- `web/src/lib/dashboard/cursor-page.ts:31` — the `DRAIN_PAGE_LIMIT` doc no
  longer claims "Every listing endpoint caps a page at 500 rows": it names the
  drain listings that do (workflows, credentials, datastores, schedules, API
  keys) and records that GET /executions and the workflow-versions listing cap at
  100 and never ask for this size.
- `web/src/routes/(dashboard)/executions/+page.svelte:204` — the stale "It also
  never touches the cursor" sentence is replaced with the true property: a poll
  rewrites the rows the head covers and can move the list's cursor when the head
  falls back to the newest page alone, which is why `loadMore` re-checks the
  cursor it asked with (`appendPageIfCurrent`).
- AC 5 reworded to state the boundary the code actually has: the older rows are
  "kept while fewer than one page of new runs (EXECUTIONS_PAGE_LIMIT) arrives
  between two polls — a whole page or more falls back to the newest page alone,
  the boundary the Known limits record". Chose the reword over making the gap
  fallback page forward from the head to the last loaded row: paging forward
  would fetch an unbounded number of extra pages to fill a gap nobody asked to
  see, and the Known limits already documented the fallback, so rewording is the
  smallest honest change and leaves AC 5, the Known limits and `mergeHead`'s own
  doc agreeing.

**Verification**

- `cd e2e && pnpm exec playwright test tests/dashboard-lists.spec.ts --retries=0`
  — `6 passed (1.1m)`.
- `... --repeat-each=3 --retries=0` — three runs: `17 passed (2.1m)` (one failure,
  the pre-existing test 3, see below), `17 passed (2.0m)` (same), `18 passed
  (2.9m)`. The new case itself passed 9/9 across those runs.
- `cd e2e && ... -g "does not cut a list" --repeat-each=5 --retries=0` — `5 passed
  (1.3m)`, and with the new case excluded the suite's repeat-each=3 run is `15
  passed (2.3m)`: the flake is in the existing test 3's `toPass` loop, whose
  `expect(pager).toHaveCount(0)` treats the Load more button's disappearance as
  "fully loaded" — but a transient Load more failure ALSO removes the button
  (replaced by the retry notice), leaving 50 rows and failing
  `expect(total).toBe(60)` with `Expected: 60 / Received: 50`. It is a
  pre-existing test weakness (this change touches no page behaviour, only
  comments), only reachable under the heavy machine load of the parallel ticket
  wave; it is left alone rather than silently re-pinned.
- `cd web && pnpm check` — `1517 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS`.
- `cd web && pnpm test` — `Test Files 44 passed (44)`, `Tests 556 passed (556)`.
- `go test ./internal/guardrails/...` — `ok github.com/kilaslab/kilas-flow/internal/guardrails 0.670s`.

**Known limits**

- Rows below the newest page keep their status until Refresh; Stop reloads from
  the top and drops the pages the user loaded.
- A whole page (50) or more of new runs between two polls shrinks the list back
  to the newest page (the gap fallback), rather than splicing across a hole.
- The executions and schedules pages still drain every workflow just to resolve
  names (about two requests at 600 workflows).
- The datastore rows grid still says "N rows on this page" while Load more
  extends it — a seventh surface, left alone here.

### Delta review round 1 — the completion probe stops inferring completion from the button

**Finding (medium).** The third e2e case, "auto-refresh does not cut a list that
was loaded to its end" (`e2e/tests/dashboard-lists.spec.ts`), decided the list was
fully loaded from the ABSENCE of the Load more button
(`expect(pager).toHaveCount(0)`). That state is also true (a) before the first
page has painted — `isVisible()`/`count()` do not auto-wait — and (b) after a
Load more failed, where `pagingFailure`
(`web/src/routes/(dashboard)/executions/+page.svelte:130-132`) removes the button
and the retry notice takes its place (`list-states.svelte`, "Try again" →
`onRetryMore`). Under load the loop could therefore exit with 0 rows (a) or 50 of
60 (b) and then fail `expect(total).toBe(60)` — the `Received: 50` seen twice.
The spec was introduced by this branch's stage 2 (`9abf95b`), so the flake is the
branch's own.

**The fix — test only, no page behaviour touched.** The loop now exits on the one
string the page renders only while it holds every row: `Showing all 60
executions.` (exact). Before the loop it waits for the first page
(`await expect(more).toBeVisible()`), so the pre-paint state cannot be read as the
end; inside the loop it presses the page's own recovery — "Try again" →
`onRetryMore` — when a page failed, bounded by `toPass({ timeout: 30_000 })`. The
count assertion is unchanged (`expect(total).toBe(PAGE + 10)`, read from the
rendered rows), nothing is retried forever, and no sleep was added.

**Proof that the exit condition is real.** A throwaway probe
(`e2e/tests/zz-probe-paging-exit.spec.ts`, deleted after the run; `git status
--short e2e/tests` shows only the spec) replayed both loops over the same seed
with the API mutated through `page.route`:

| probe | mutation | loop | result |
| --- | --- | --- | --- |
| T1 | first page delayed 3 s | fixed | `1 passed` — the loop waits out the pre-paint window |
| T2 | first page delayed 3 s | pre-fix | `Expected: 60 / Received: 0` — defect (a), reproduced |
| T3 | cursor page 500s | pre-fix | `Expected: 60 / Received: 50` — defect (b), the observed flake |
| T4 | cursor page 500s | fixed | red: `getByText('Showing all 60 executions.')` never appears — a genuinely unfetchable second page still fails, it is not passed by retrying forever |

**Proof that the flake is gone.** `cd e2e && pnpm test dashboard-lists
--repeat-each=3 --retries=0`, twice on the loaded machine (the second run with
`cd web && pnpm check && pnpm test` running alongside): run 1 `18 passed (2.2m)`,
run 2 `18 passed (2.1m)`; the JSON report reads `expected: 18, unexpected: 0,
flaky: 0` for both, i.e. three repeats of each of the six cases, zero retries.

**Gates.** `cd web && pnpm check` — `1517 FILES 0 ERRORS 0 WARNINGS 0
FILES_WITH_PROBLEMS`; `cd web && pnpm test` — `Test Files 44 passed (44)`,
`Tests 556 passed (556)`; `go test -count=1 ./internal/guardrails/...` — `ok
github.com/kilaslab/kilas-flow/internal/guardrails 0.833s`. No Go, API, SDK,
config, migration or dialect surface changed, so those gates do not apply, and no
dependency was added.
