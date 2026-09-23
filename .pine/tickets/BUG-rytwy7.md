---
id: BUG-rytwy7
title: Datastore row search starts an endless request loop (~2,600 req/s) and freezes the page
status: done
priority: critical
labels:
    - datastore
    - regression
    - performance
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T04:13:59Z"
---

# Description

Typing one character into Search rows sends GET /datastores/{id} and /rows back to back until the user leaves the page, peaking at 2,642 requests per second. This is a self-inflicted DoS that slows every user of the instance. It is a regression from BUG-esb9sh.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-1). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The data table search filters rows once per keystroke, debounced.

# Steps to Reproduce

1. Open /datastores/<id> for a table that has columns and rows (I used "[ux-ops] Leads": 4 columns, 46 rows). 2. Type one character, e.g. "u", into "Search rows". 3. Watch the network log or the server log.

# Expected

One debounced row query per search change, and the page stays interactive.

# Actual

The page goes to "Loading datastore…" and does not come back. The search box unmounts. The browser sends GET /api/v1/datastores/{id} and GET /rows?limit=20&match=any&columnName=email&condition=ilike&value="%u%" back to back without stopping: 23,678 → 25,882 requests in 3 s in the playwright log, with a peak of 2,642 requests in one second in kf-server.log. The loop ends only when you leave the page. It reproduced twice (08:10:57–08:11:05 and 08:11:49–08:11:53). A self-inflicted DoS like this slows every other user of the instance.

# Acceptance Criteria
- [x] The page's load `$effect` is keyed on `id` only (loads wrapped in `untrack`)
- [x] Search runs through its own debounced effect: one row query per settled change
- [x] A refetch does not flip `detailLoading` or unmount the search box
- [x] A component or e2e test types into search and asserts a bounded number of requests

## Progress

Fixed in `web/src/routes/(dashboard)/datastores/[id]/+page.svelte`:
- The load effect is now keyed on `id` only, with `loadDetail`/`load` calls wrapped in `untrack` (matches the pattern at `executions/[id]/+page.svelte:119-127`).
- Added a second effect that reads `search` and `id`, skips its own first (mount) run, and otherwise sets a 250 ms `setTimeout` that calls `untrack(() => load(id))`, returning `clearTimeout` as cleanup.
- Removed the direct `void load(id)` call from the search input's `oninput`, so search only ever triggers a fetch through the debounced effect.
- `loadDetail` now only sets `detailLoading = true` when `detail === null` (first load), so a background refetch (column add/rename/delete, "Try again") no longer flips the skeleton or unmounts the search box.

Proven by a new Playwright test, `e2e/tests/datastore.spec.ts` — "typing into row search is debounced and never loops (BUG-rytwy7)": seeds a datastore with columns and rows, counts GET requests to the detail and `/rows` endpoints across mount + typing "u" + a 2 s settle, and asserts at most 2 `/rows` requests and exactly 1 detail request, plus that the search input keeps its typed value and focus throughout.
- RED: against the pre-fix component, the test captured 46 `/rows` requests fired back-to-back inside the wait window (i.e., a live reproduction of the reported loop) and failed.
- GREEN: against the fixed component, `pnpm exec playwright test tests/datastore.spec.ts` — 9 passed, including this test.
- Also verified: `cd web && pnpm check` (0 errors) and `cd web && pnpm test` (624 passed).

### Fix round 1

Review found the round-1 fix's debounced-search guard (`searchEffectRan`, a one-shot boolean) only
ever skipped the very first mount. SvelteKit reuses the `[id]/+page.svelte` instance across a
param-only navigation (no `{#key id}` wrapper), so on every later `id` change the debounced effect
still queued its own 250 ms `load(newID)` on top of the id-effect's immediate one — a second,
redundant fetch per table switch. Separately, `detail` was never reset on an `id` change, so with
`detailLoading` already `false` from the previous table, the skeleton was skipped and the old
table's name/columns could show until the new `loadDetail` response landed.

Fixed in the same file:
- The debounce guard is now `lastSearchID: string | null`, compared against the current `id` on
  every run, rather than a one-shot flag — an `id` change updates it and skips scheduling a timer
  (the id-effect already loads), while a same-`id` search change still debounces normally.
- The id-effect now resets `detail = null` and `search = ''` before its untracked loads, so a
  datastore switch always re-shows the skeleton and never carries a stale search term into a table
  it was never typed against.

Covered by a new Playwright test — `e2e/tests/datastore.spec.ts`, "a client-side navigation between
two datastores loads the new one once and drops the old one (BUG-rytwy7)": seeds two datastores,
loads the first, then performs a genuine client-side navigation to the second (clicking an injected
in-app `<a>`, since `page.goto()` is a hard reload and going through the `/datastores` list would
destroy and recreate the component instead of reusing it — neither reaches the reused-instance
path). Asserts the second table's name/row show, the first table's name is gone, and exactly one
GET to the second table's `/rows` endpoint was made.
- RED (against the round-1 fix, temporarily restored via `git show HEAD:...`): 2 GET requests to
  the second table's `/rows` — the immediate load plus the redundant debounced one.
- GREEN (fixed): `cd e2e && pnpm exec playwright test tests/datastore.spec.ts` — 10 passed.
- Also verified: `cd web && pnpm check` (0 errors) and `cd web && pnpm test` (624 passed).

# Implementation Plan

Key the effect on `id` only (wrap the loads in `untrack`). Run the search through its own debounced effect that calls load(), and don't flip detailLoading on a refetch.

# Notes

Related tickets: BUG-esb9sh

Related (from the audit): BUG-esb9sh (done). Its commit df6c87c added this search, so this is a regression.

# Related Files

agents/ux-ops/32-datastore-search-loop.png; agents/ux-ops/datastore-loop-log-excerpt.txt (requests per second); kf-server.log lines 17k–61k. Root cause: web/src/routes/(dashboard)/datastores/[id]/+page.svelte:135-140 runs `$effect(() => { if (id) { void loadDetail(id); void load(id); } })`. load() calls listParams() synchronously, before its first await (:181-196). Once search is non-empty, listParams() reads `userColumns`, which derives from `detail`, so the effect starts tracking `detail`. loadDetail() then reassigns `detail` (:148) and sets `detailLoading = true`, which triggers the effect again, and so on forever. With an empty search listParams returns early, so the loop never shows up without a search.

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23. Rewritten on 2026-09-23 to list only this ticket's own commits (`git log --grep "BUG-rytwy7" 2f8e9b1..5830b4e`) and the files exactly those commits changed.

- Range: `2f8e9b1..5830b4e`
- Commits (3):
  - `0b0d36e7` — BUG-rytwy7: switching datastores no longer double-fetches or flashes the old one
  - `da6593c5` — chore(pine): close BUG-rytwy7 with its landing evidence
  - `8eaa1911` — BUG-rytwy7: datastore row search debounces instead of looping forever
- Merged by (1):
  - `1561296b` — merge: datastore search settles after one query, and an impossible cron is refused instead of firing (BUG-rytwy7, BUG-g7ffj1)
- Files changed by those commits (merge commits excluded):

```
 .pine/tickets/BUG-rytwy7.md                        | 327 ++++++++++++++++++++-
 e2e/tests/datastore.spec.ts                        | 103 +++++++
 .../(dashboard)/datastores/[id]/+page.svelte       |  75 ++++-
 3 files changed, 482 insertions(+), 23 deletions(-)
```
