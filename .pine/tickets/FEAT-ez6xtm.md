---
id: FEAT-ez6xtm
title: 'Node states & actions: disabled nodes look active and can''t be re-enabled; no context menu, notes or pin'
status: doing
priority: high
labels:
    - editor
    - canvas
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:48:14Z"
updated: "2026-11-27T08:51:29Z"
---

# Description

The runner passes items through a disabled node, but the canvas draws it as active and offers no way to toggle it, so the user sees a node that "does nothing".

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-4). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** `D` deactivates a node, which is then greyed out and struck through. `P` pins data. Right-click opens a context menu (open, execute step, rename, deactivate, pin, copy, duplicate, tidy up, delete). Settings has Notes and "Display note in flow".

# Steps to Reproduce

1. POST SD/n8n-disabled.json to /api/v1/workflows/import (it has `"disabled": true` on "Disabled Set" plus a note). This created wf_01a0cbed-d122-75eb-8aa3-d4075c3db9bf. 2. Open it. 3. Select "Disabled Set": press D, right-click it, and open its Settings tab. 4. `kilasflow run <id> --wait`.

# Expected

Disabled nodes are visibly dimmed, with a toggle (D, node toolbar, context menu). Pinning and notes are available too.

# Actual

The document has `disabled: true` and the runner honours it (the Set passes `{}` through unchanged), but the tile is drawn at full opacity with no marker. Settings shows only Continue on Fail / Retry / Timeout / Always Output Data, so nothing can re-enable the node without editing JSON. D, P and right-click do nothing, and there is no context menu on nodes or the pane. The node's note was dropped. A user sees a Set node that "does nothing".

# Acceptance Criteria
- [x] Disabled nodes are visibly dimmed or struck through
- [x] Disable/enable is available with D, from the node toolbar and from the context menu
- [ ] A right-click context menu offers open, rename, duplicate, disable, pin, copy and delete
- [x] Node notes can be edited and shown on the canvas (with notes round-trip, see the round-trip ticket)

# Implementation Plan

Render `node.disabled` (opacity plus strike-through), add a toggle in the node toolbar and the Settings tab and bind `D`, and add a node/pane context menu that reuses the existing actions.

# Notes

Related tickets: FEAT-56nep4, FEAT-jvembs

Related (from the audit): FEAT-jvembs and FEAT-56nep4 (both done, with these items unchecked or listed as "Not done"). Pin data: UXD-3.

# Related Files

SD/78-imported-disabled-node.png, SD/disabled.js, SD/ctxmenu.js (`{"nodeContextMenu":[],"paneContextMenu":[]}`). web/src/lib/components/workflow-editor/canvas-node.svelte has no `disabled` handling. shortcuts.ts has no d/p.

# Attachments

## Progress

2026-11-27, on the integration tip af29d911. The ticket text predates part of this: D, P, `toggleNodesDisabled`, the pinned marker and an `opacity-45 grayscale` dim on disabled tiles were already on the branch, so only the missing parts were built.

Done (all verified in a real browser through `e2e/tests/node-states.spec.ts`, 7 tests, and by `pnpm check` + `pnpm vitest run`):

1. Disabled look: the name is now struck through (`line-through`), the tile has a tooltip and the name a screen-reader "(disabled)" cue, on top of the existing dim.
2. Disable/enable: D (already bound), a new button in the node toolbar (Power / PowerOff, label flips between Disable and Enable), the context menu, and a Disabled checkbox in the Settings tab (a "Node state" group, hidden for sticky notes and read-only canvases). The flag is dropped, not written `false`, when enabling.
3. Context menu (`node-context-menu.svelte`, rules in `node-menu.ts`): right click, or Shift+F10 / the context-menu key on a focused node. `role="menu"` with `menuitem` rows: Open, Rename, Duplicate, Disable or Enable, Copy, Delete, each running the editor's existing action. A node inside the selection acts on the whole selection; one outside it is selected first. Read-only canvases get only Open and Copy. Arrow keys wrap, Home/End jump, Escape and Tab close it and return focus to the node, an outside press closes it without taking focus, and it keeps the canvas shortcuts off while open.
4. Notes: the document already carries them. The n8n importer/exporter keeps `notes` and `notesInFlow` in the node's `settings` (`withNotes`/`applyNotes` in `internal/interop/n8n/n8n.go`, covered by n8n_test.go), and the canonical `Node` has no field of its own for them. The editor reads and writes those same two keys (`node-state.ts`): Notes textarea and "Display note in flow" checkbox in Settings, and the note is drawn under the node on the canvas (plain text, clamped to three lines) when the flag is on and the note is not blank. Clearing the note and unsetting the flag removes the keys, and `settings` itself when it empties. Nothing changed on the Go side or in the API models.

Not done:

- Pin in the context menu (criterion 3 lists it). Left out on purpose: `P` already toggles a pin, but it pins an empty output, and a menu item that silently makes a run replay nothing for the node belongs with FEAT-70j6dn's pin UI (view, edit, unpin). That is why criterion 3 stays unticked; the rest of it is done.
- A pane context menu (the ticket's plan mentions one; no criterion needs it).
- The replay canvas (`execution-canvas-node.svelte`) does not draw the disabled look.

Not exercised by any test: the menu's behaviour on a touch screen and on a narrow layout, where selecting the node also opens the inspector as a modal.

Also learned: a `line-clamp` element with its own padding shows the top of the line it cut, so the note's padding is on a box around the clamped text.
