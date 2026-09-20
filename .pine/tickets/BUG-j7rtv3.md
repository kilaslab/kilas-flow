---
id: BUG-j7rtv3
title: Editor page throws effect_update_depth_exceeded (canvas projection ↔ selection identity loop)
status: done
priority: medium
labels:
    - editor
    - canvas
    - regression
created: "2026-09-20T01:46:34Z"
updated: "2026-09-20T02:04:01Z"
---

Found by ImportDiagnostics (BUG-f9frth): the editor page could not show anything that
arrived after the first flush — the stored import report included — because the page
aborted with `effect_update_depth_exceeded` on every load. Pre-existing, reproduced on
main without the BUG-f9frth page changes and with a hand-built workflow.

## Root cause

`web/src/lib/components/workflow-editor/workflow-editor.svelte`

The canvas projection `$effect` reads `selectedNodeIDs`, and `onSelectionChange` — the
handler Svelte Flow calls — assigned it a fresh array on every event. Flow answers a
replaced `nodes` array with a selection event of its own, so the effect re-ran on the
output of its previous run:

    projection → Flow selection event → fresh array → projection → …

The flush never drained. Every write queued behind it (the import report's `importReport`
among them) stayed out of the DOM, and Svelte aborted the flush with
`effect_update_depth_exceeded`. No guard that swallows the error can fix this: the cycle
has to be cut, not muted.

## Fix

Commit `a62ba8f` — "BUG-j7rtv3: fix the editor's selection/projection loop".

The selection is adopted only when what it holds really changed (`sameSelection`): an
event that names the same nodes writes nothing, so the feedback edge is cut at its
source. The comparison is order-insensitive, because Flow may report the same selection
in another order and treating that as a change is what closed the loop. The primary
selection (`selectedNodeID`) follows the *held* array, so the state stays internally
consistent while the set is unchanged.

## Reproduction (before/after, same page, same data, dev SPA)

Live API (`go run ./cmd/kilasflow`, sqlite `data/kilasflow.db`) + `vite dev` on 5273,
workflow imported over `POST /api/v1/workflows/import` from an n8n file with an unknown
node (`wf_01a0bc7f-2d28-78b6-a565-1473fae932e3`, revision `wfv_01a0bc7f-2d28-7ba0-82b0-f708ee798d6b`).

Chromium, 3 loads each, counting CDP `Runtime.exceptionThrown` / `pageerror`:

| | `effect_update_depth_exceeded` | `[data-import-diagnostic]` | report button |
|---|---|---|---|
| fix reverted (control) | 1 per load, 3/3 | 0 | 0 |
| fix in place | 0, 3/3 | 1 (`blocking`) | 1 ("Import report · 1") |

The control was produced by reverting only the guarded assignment in the same working
tree and restoring it afterwards, so both arms ran the same server, database and page.

Interactions after the fix: the toolbar button opens the drawer with the stored report
("Imported with a hole was imported from n8n … 1 blocking issue"), clicking a node selects
it and opens the inspector, clicking the pane clears the selection. No errors during the
whole sequence.

Visual: `/tmp/kflow-editor-after-fix.png` — breadcrumb button "Import report · 1" and the
red blocking badge on the Mystery node.

## Evidence

- Badge: `data-import-diagnostic="blocking"`, aria-label
  `1 blocking import issue on Mystery. Open the import report.`
- `GET /api/v1/workflows/{id}/diagnostics?versionId=…` → 200 for both arms; only the fixed
  arm rendered it.
- `npx vitest run src/lib/workflow-editor` → 32 files, 373 tests passed.
- `npx svelte-check --tsconfig ./tsconfig.json` → 1517 files, 0 errors, 0 warnings.

## Notes

- No regression test was added inside `web/`: the invariant is a component effect loop, and
  the frontend package has no component-test harness (no jsdom/`@testing-library/svelte` in
  `devDependencies`), so the proof is the browser A/B above. The natural home for a
  permanent guard is the Playwright suite at `e2e/` (it boots the real binary with the
  embedded SPA), which needs `make build-all` — that belongs to the parent's project-wide
  validation, not to this scoped change.
- The unrelated 401 on `/api/v1/auth/me` in the console is the auth probe on a server with
  authentication disabled; it predates this change.
- The stacked port labels on the unsupported "Mystery" placeholder tile are cosmetic and
  unrelated (they render identically before and after).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `5709b4d2` (last commit at or before ticket created 2026-09-20)
- Commits (2):
  - `31c424f8` — chore(pine): file editor-loop, conditions-coercion, paging and follow-up tickets
  - `a62ba8f3` — BUG-j7rtv3: fix the editor's selection/projection loop
- Files changed (base → working tree):

```
 .pine/tickets/BUG-1tj5wy.md                        | 454 +++++++++++++++-
 .pine/tickets/BUG-277a2m.md                        | 454 +++++++++++++++-
 .pine/tickets/BUG-341sxn.md                        |  40 ++
 .pine/tickets/BUG-4053h6.md                        | 464 ++++++++++++++++-
 .pine/tickets/BUG-57n76x.md                        | 455 +++++++++++++++-
 .pine/tickets/BUG-66es9z.md                        | 248 +++++++++
 .pine/tickets/BUG-6as5y7.md                        | 453 +++++++++++++++-
 .pine/tickets/BUG-6bqh51.md                        | 455 +++++++++++++++-
 .pine/tickets/BUG-6jvcs5.md                        | 455 +++++++++++++++-
 .pine/tickets/BUG-8dmp5y.md                        | 460 ++++++++++++++++-
 .pine/tickets/BUG-8h4yy1.md                        | 454 +++++++++++++++-
 .pine/tickets/BUG-8sb0jw.md                        | 460 ++++++++++++++++-
 .pine/tickets/BUG-8t94wn.md                        | 453 +++++++++++++++-
 .pine/tickets/BUG-9853ay.md                        | 454 +++++++++++++++-
 .pine/tickets/BUG-a1648n.md                        |  85 ++-
 .pine/tickets/BUG-a9mp5a.md                        | Bin 10476 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        | 472 ++++++++++++++++-
 .pine/tickets/BUG-c241hm.md                        | 457 ++++++++++++++++-
 .pine/tickets/BUG-cq4yk3.md                        | 461 ++++++++++++++++-
 .pine/tickets/BUG-dndnhn.md                        | 453 +++++++++++++++-
 .pine/tickets/BUG-esb9sh.md                        | 456 +++++++++++++++-
 .pine/tickets/BUG-f9frth.md                        | 571 ++++++++++++++++++++-
 .pine/tickets/BUG-fv5fer.md                        | 467 ++++++++++++++++-
 .pine/tickets/BUG-gaavr5.md                        | 454 +++++++++++++++-
 .pine/tickets/BUG-hfhzq6.md                        | 456 +++++++++++++++-
 .pine/tickets/BUG-hm76dq.md                        | 454 +++++++++++++++-
 .pine/tickets/BUG-j7rtv3.md                        |  90 ++++
 .pine/tickets/FEAT-15k49d.md                       |  37 ++
 .pine/tickets/FEAT-cwmw90.md                       |  21 +
 .pine/tickets/FEAT-jvembs.md                       |   4 +-
 internal/interop/n8n/corpus/BASELINE.md            |  16 +-
 internal/interop/n8n/corpus/baseline.json          |  43 +-
 .../workflow-editor/workflow-editor.svelte         |  31 +-
 33 files changed, 10707 insertions(+), 80 deletions(-)
```
