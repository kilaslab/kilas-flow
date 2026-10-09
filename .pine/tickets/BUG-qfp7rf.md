---
id: BUG-qfp7rf
title: A connection a node's ports no longer have stays in the document but is never drawn, so it cannot be selected or deleted
status: done
priority: medium
labels:
    - editor
    - canvas
parent: EPIC-8rbys7
created: "2026-10-09T13:43:17Z"
updated: "2026-10-09T13:53:32Z"
---

# Description

When a node's configuration changes its ports, a connection that names a port the node no longer has stays in the draft and in the saved document, but the canvas cannot draw it: Svelte Flow draws an edge only when both handles exist. The author sees a workflow with a wire missing, cannot select the wire, and so cannot delete it. The only trace is the compiler's "connection must reference declared source and target ports", and only when the workflow is activated, run, or sent to `POST /workflows/validate` (a save does not compile).

The cases:
- Changing a vector store's Mode (retrieve to retrieve-as-tool drops the `vectorStore` output, insert has no `main` input, ...). Found while fixing BUG-pc284d.
- A Switch that loses a rule (its `2` output goes away) or a Merge whose input count is lowered (`input3`, `input4` go away).
- Turning off "continue using error output" on a node whose `error` output is wired.

`resolvedPorts` (`web/src/lib/workflow-editor/ports.ts`) decides which handles exist; nothing compares the document's connections against it when a property changes (`updateProperty` / `updateNodeProperty` replace the node and leave `connections` alone).

# Steps to Reproduce

1. Open a workflow with a Postgres PGVector Store in Retrieve mode wired to a Vector Store Tool (`vectorStore` to `vectorStore`).
2. In the inspector, change Mode to "Retrieve as Tool".
3. Look for the wire between the store and the tool, then try to select and delete it. Press Execute.

# Expected

The editor never leaves a wire in the document that it cannot show. When a change takes away a port that carries wires, the wires that no longer fit are removed in the same undoable step as the change, and the editor says which ones went (and that Undo brings them back). The same for a Switch rule, a Merge input and an error output.

# Actual

The wire is invisible and unselectable but still stored; Execute or Activate fails with "connection must reference declared source and target ports" naming a connection the author cannot see. Changing the Mode back makes the wire reappear, silently.

# Acceptance Criteria
- [x] A pure function returns the connections of a node whose source or target port its current configuration no longer resolves (vitest: vector store modes, Switch rules, Merge inputs, error output; connections of other nodes and valid connections are untouched)
- [x] After a property change that strands wires, the editor removes them in the same history step as the change (one Undo restores both the property and the wires), and shows what was removed in the status line, in both locales
- [x] A typed edit that passes through a smaller port count (clearing a Merge's input count before typing the new one) does not destroy wires: removal waits for the edit to settle, and a transient state is never applied
- [x] vitest for the removal and for the settle timing; e2e (`vector-store-ports.spec.ts`) changes the Mode, sees the notice, sees the wire gone from the saved document, and undoes back to the wire

# Implementation Plan

Add `staleConnections(document, nodeID, definitions)` to `ports.ts`. In `workflow-editor.svelte` `updateProperty`, after the property is written, schedule a short settle timer per node (shorter than the history coalesce window, so the removal joins the edit's undo step); when it fires, remove the stale connections with the same history key and set `canvasMessage` (the existing status line). Copy goes in `web/messages/en/editor.json` and `id/editor.json`.

# Notes

Found in review of BUG-pc284d (2026-10-09). The settle delay exists because `numberInputs` and other typed fields commit on every keystroke: backspacing "4" to type "6" passes through a Merge with two inputs, and removing wires at that instant would destroy `input3` and `input4`. Related: BUG-pc284d, BUG-sgrxhh (the error output).

# Related Files
- web/src/lib/workflow-editor/ports.ts — resolvedPorts
- web/src/lib/components/workflow-editor/workflow-editor.svelte — updateProperty
- web/src/lib/workflow-editor/document.ts — updateNodeProperty
- web/messages/en/editor.json
- web/messages/id/editor.json
- e2e/tests/vector-store-ports.spec.ts

## Progress (2026-10-09)

State when the session stopped (branch worktree-agent-ab728406a999eaf54):

Done and committed (e0a50b4d, vitest 1178 pass, pnpm check 0 errors), not yet called by the editor:
- `staleConnections(document, nodeID, definitions)` and `describeConnections(connections, nodes, limit)` in `web/src/lib/workflow-editor/ports.ts`. Tests: `ports.stale.test.ts` (vector store modes, insert store main input, Switch rule, Merge input count including the transient value 1, error output, other nodes untouched, unknown definition not judged).
- `createPortSettler` / `PORT_SETTLE_MS` (500 ms) in `web/src/lib/workflow-editor/port-settle.ts`: one timer per node, restarts on each edit, runs with the edit's history key; shorter than the history coalesce window (700 ms) so the removal joins the edit's undo step. Tests: `port-settle.test.ts`, including a `record()` check that the removal coalesces into one history entry.
- Copy: `editor_connections_removed` (plural, params count / node / detail) in `web/messages/en/editor.json` and `id/editor.json`.

In the working tree, NOT committed: `web/src/lib/components/workflow-editor/workflow-editor.svelte` — `updateProperty` calls `portSettler.schedule(nodeId, historyKey)` after the edit; `removeStrandedConnections(nodeID, key)` removes `staleConnections(draft, ...)` with `replaceDraft(..., { key })` (same key, so one undo step), drops them from `selectedEdgeIDs`, and sets `canvasMessage` (the status line) to the new message; `const portSettler = createPortSettler(removeStrandedConnections)` with `$effect(() => () => portSettler.cancel())`; `undo()` and `redo()` clear `canvasMessage` (a notice saying "Undo brings it back" is stale once undone). `pnpm check` and `pnpm test` were green with it.

Remaining:
1. Update `e2e/tests/vector-store-ports.spec.ts`, which still asserts the OLD behaviour (the stale wire stays in the saved document and Execute reports it) and will fail once the editor hunks land. New flow: select Mode "Retrieve as Tool", expect the handles, expect a status line containing "Removed 1 connection that Policies Store has no port for any more" and "Policies Store vectorStore → Handbook Tool vectorStore" (it appears after about 500 ms); click Undo and expect the Mode select back on "retrieve", the `vectorStore` handle and the `c-store` edge back, and the notice gone; select "Insert Documents", wait for the notice again, Save, and fetch the stored workflow: `c-store` must be absent and `c-embeddings` present.
2. Commit the editor hunks and the spec together, run the spec (`cd e2e && pnpm exec playwright test tests/vector-store-ports.spec.ts`, rebuild needed for the web change), and tick the criteria.
3. Not covered yet by a vitest at component level: the typed-edit case (a Merge's input count) is covered only by `port-settle.test.ts` and `ports.stale.test.ts`; consider a manual check in the editor by backspacing the number.

### Finished (2026-10-09, 4c2e5539)

The remaining steps above are done; the "In the working tree, NOT committed" paragraph is superseded. `updateProperty` schedules the settler, `removeStrandedConnections` removes the stranded wires with the edit's history key and sets the status line, and `undo()` / `redo()` clear the notice. Criteria, with evidence:
- Pure finder: `ports.stale.test.ts` (vector store modes, insert store main input, Switch rule, Merge inputs including the transient value 1, error output, other nodes untouched, unknown definition not judged).
- Removed in the edit's own undo step, with a notice in both locales: e2e `vector-store-ports.spec.ts` changes Mode to Retrieve as Tool, sees "Removed 1 connection that Policies Store has no port for any more: Policies Store vectorStore → Handbook Tool vectorStore", presses Undo and gets the mode, the handle and the wire back with the notice gone, then changes the mode again and finds the wire absent from the saved document. `port-settle.test.ts` shows by `record()` that the removal coalesces into one history entry. The Indonesian copy (`web/messages/id/editor.json`) exists and passes the catalog parity test, but no e2e reads it.
- Typed edits do not destroy wires: removal waits `PORT_SETTLE_MS` (500 ms) after the last edit of the node (`port-settle.test.ts`, fake timers). Not exercised in a browser: nobody has backspaced a Merge's input count in the running editor, so that remains a manual check.
Also run green: `pnpm check` (0 errors), `pnpm test` (80 files, 1178 tests), the two new/changed specs and 18 neighbouring editor tests against a `make build-all` binary.
Known limit: only the edited node's own wires are judged, so a stranded wire that arrives any other way (an import, a paste) is still reported only by the compiler on activate or run.

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `03b191e8` (last commit at or before ticket created 2026-10-09)
- Commits (7):
  - `05083cd0` — merge: the slot '+' offers and wires the right providers, and a wire a node's ports no longer have is removed in the edit's own undo step, with ports goldens that track every mode option (BUG-w1jp70, BUG-qfp7rf, BUG-pc284d review fixes)
  - `aeda0e14` — chore(pine): BUG-w1jp70 and BUG-qfp7rf record what was done, with evidence
  - `4c2e5539` — BUG-w1jp70, BUG-qfp7rf: a node picked for an attachment slot is wired from its output and placed under its consumer, and wires a mode change strands are removed in the edit's undo step with a notice
  - `2848c0a1` — chore(pine): BUG-w1jp70, BUG-qfp7rf and FEAT-q80xe3 reach the board, with their partial work waiting on branch worktree-agent-ab728406a999eaf54
  - `1f3ce489` — chore(pine): record the state and remaining steps of BUG-w1jp70, BUG-qfp7rf and FEAT-q80xe3
  - `e0a50b4d` — BUG-w1jp70, BUG-qfp7rf: the slot picker filter, the picked-node wiring and position, the stranded-connection finder and the settle timer exist as tested pure functions, not yet used by the editor
  - `479e0b08` — chore(pine): file BUG-w1jp70 (slot + picker is empty and wires the wrong way), BUG-qfp7rf (stranded connections are never drawn) and FEAT-q80xe3 (picker offers stores per mode role), found in review of BUG-pc284d
- Files changed (base → working tree):

```
 .pine/tickets/BUG-pc284d.md                        | 483 ++++++++++++++++++++-
 .pine/tickets/BUG-qfp7rf.md                        |  84 ++++
 .pine/tickets/BUG-w1jp70.md                        | 120 +++++
 .pine/tickets/FEAT-q80xe3.md                       |  65 +++
 .pine/tickets/FEAT-vntngh.md                       |  56 +++
 e2e/tests/attachment-slot-picker.spec.ts           |  55 +++
 e2e/tests/vector-store-ports.spec.ts               |  33 +-
 web/messages/en/editor.json                        |  10 +
 web/messages/id/editor.json                        |  15 +
 .../workflow-editor/workflow-editor.svelte         |  67 ++-
 web/src/lib/workflow-editor/catalog.test.ts        |  26 ++
 web/src/lib/workflow-editor/catalog.ts             |  10 +-
 web/src/lib/workflow-editor/document.test.ts       |  20 +
 web/src/lib/workflow-editor/document.ts            |  17 +
 web/src/lib/workflow-editor/port-settle.test.ts    |  71 +++
 web/src/lib/workflow-editor/port-settle.ts         |  42 ++
 web/src/lib/workflow-editor/ports.picked.test.ts   | 126 ++++++
 web/src/lib/workflow-editor/ports.stale.test.ts    | 159 +++++++
 web/src/lib/workflow-editor/ports.ts               |  89 +++-
 19 files changed, 1516 insertions(+), 32 deletions(-)
```
