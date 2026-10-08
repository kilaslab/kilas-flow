---
id: BUG-pzkpfr
title: 'Typing in the inspector re-projects the whole canvas: keystroke latency 29→98 ms from 50→300 nodes'
status: done
priority: medium
labels:
    - editor
    - performance
    - regression
parent: EPIC-8rbys7
created: "2026-09-23T01:48:15Z"
updated: "2026-09-23T01:48:15Z"
---

# Description

Load, pan and zoom stay at 60 fps at 300 nodes. Typing is the one thing that degrades with size. BUG-qmgz2f halved the cost, but it is still O(n).

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-15). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Typing latency in the NDV does not depend on workflow size.

# Steps to Reproduce

1. Open "[ux-editor] Perf 300 nodes". 2. Open "set 1" and type into its field 200 ms apart (SD/typeperf.js), and in a burst (SD/editperf.js).

# Expected

Under 50 ms per keystroke at any size.

# Actual

Median keystroke-to-frame latency is 29 ms at 50 nodes, 55 ms at 150 and 98 ms at 300. At 300 nodes, 11 of 12 keystrokes are 50-66 ms long tasks. A 20-key burst reaches a median of 123 ms and p95 of 251 ms, and Cmd+Z → frame takes 97 ms. Load, pan and zoom stay at 60 fps (see perf.md), so this is the only size-dependent cost.

# Acceptance Criteria
- [x] A parameter keystroke updates only the edited node's projection
- [x] Median keystroke latency is under 50 ms at 300 nodes, measured with the audit's perf script and recorded

# Implementation Plan

Patch only the changed node in `nodes` (keyed update), or debounce the projection of parameter edits (the canvas only shows the subtitle).

# Notes

Related tickets: BUG-qmgz2f

Related (from the audit): BUG-qmgz2f (done; measured 211 ms at 400 nodes and claimed a fix; halved but still O(n))

# Related Files
- web/src/lib/workflow-editor/document.ts
- web/src/lib/workflow-editor/canvas-projection.test.ts
- web/src/lib/components/workflow-editor/workflow-editor.svelte

SD/perf.md section 4, SD/perf-typing.json. workflow-editor.svelte:381-386: the `$effect` rebuilds `documentFromCanvas(displayed, definitions, issues)` and maps every node on each draft change.

# Attachments

## Fix

**Cause.** The projection `$effect` rebuilt every node and edge object from the draft on each keystroke. Svelte Flow keeps its measurements on the user node objects (it replaces each one with `{...node, measured}`), so 300 fresh objects meant 300 nodes re-adopted with no `measured`: all of them were re-measured and re-rendered, which showed up as two long tasks per keystroke.

**Fix.** `reconcileCanvas(held, document, definitions, issues, selection)` in `document.ts` projects against the arrays Svelte Flow currently holds. A node or connection is reused, as the very object Flow holds (measurements included), when its `data` was projected from the same document object, with the same catalogue and the same validation message (and, for an edge, the same aria label, so a rename relabels its wires). Only what changed is rebuilt. Arrays in which nothing changed come back identical, and the selection flag is written only onto the objects whose flag disagrees. `documentFromCanvas` now shares the per-node and per-edge builders, so the two cannot drift. Placeholder nodes are never reused, because their ports come from the connections. The editor's projection effect tracks only `displayed`, `definitions` and `issues`, and reads the held arrays and the selection untracked.

**Tests:** `canvas-projection.test.ts` (8): an edit to n7 in a 300-node chain rebuilds only n7, keeps `measured` and hands back the same edge array. An unchanged document returns the same arrays. A rename relabels only c2 and c3. Validation messages rebuild only their own node or edge. Selection touches only the objects whose flag differs. Add and remove work. The output matches `documentFromCanvas` exactly, and a new catalogue rebuilds everything. `pnpm vitest run src/lib/workflow-editor src/lib/components/workflow-editor`: 548 tests pass. `pnpm check`: 0 errors.

**Playwright (built binary, Chromium 1440×900, "set 1" assignment value field, 254 nodes rendered with the inspector open).** Latency is keydown `timeStamp` to the first task after the next animation frame. "Paced" is 12 keys 200 ms apart; "burst" is 20 keys 15 ms apart.

| build | nodes | paced median / p95 | long tasks (paced) | burst median / p95 |
|---|---|---|---|---|
| main (before) | 300 | 105.9 / 113.7 ms | 14 (median 56 ms) | 205.3 / 214.3 ms |
| main (before) | 50 | 21.3 / 24.5 ms | 0 | 19.3 / 23.3 ms |
| this fix | 300 | **23.7 / 26.2 ms** | **0** | **19.4 / 20.7 ms** |
| this fix | 50 | 4.7 / 15.4 ms | 0 | 4.0 / 4.3 ms |

Event Timing (`keydown`/`input` duration) at 300 nodes went from a 120 ms median on main to 24 ms. A MutationObserver on `.svelte-flow__nodes` during typing saw only `n1`'s subtree change. An intermediate build with BUG-3k12ky alone, where the node was opened by double-click, measured 204.6 ms at 300 nodes; it was not investigated further because this projection supersedes that path. Select-all, edge selection and Delete were re-checked on this build and still pass.

