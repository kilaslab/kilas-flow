---
id: BUG-qfp7rf
title: A connection a node's ports no longer have stays in the document but is never drawn, so it cannot be selected or deleted
status: todo
priority: medium
labels:
    - editor
    - canvas
parent: EPIC-8rbys7
created: "2026-10-09T13:43:17Z"
updated: "2026-10-09T13:43:17Z"
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
- [ ] A pure function returns the connections of a node whose source or target port its current configuration no longer resolves (vitest: vector store modes, Switch rules, Merge inputs, error output; connections of other nodes and valid connections are untouched)
- [ ] After a property change that strands wires, the editor removes them in the same history step as the change (one Undo restores both the property and the wires), and shows what was removed in the status line, in both locales
- [ ] A typed edit that passes through a smaller port count (clearing a Merge's input count before typing the new one) does not destroy wires: removal waits for the edit to settle, and a transient state is never applied
- [ ] vitest for the removal and for the settle timing; e2e (`vector-store-ports.spec.ts`) changes the Mode, sees the notice, sees the wire gone from the saved document, and undoes back to the wire

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
