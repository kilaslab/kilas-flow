---
id: FEAT-ez6xtm
title: 'Node states & actions: disabled nodes look active and can''t be re-enabled; no context menu, notes or pin'
status: todo
priority: high
labels:
    - editor
    - canvas
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:48:14Z"
updated: "2026-09-23T01:48:14Z"
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
- [ ] Disabled nodes are visibly dimmed or struck through
- [ ] Disable/enable is available with D, from the node toolbar and from the context menu
- [ ] A right-click context menu offers open, rename, duplicate, disable, pin, copy and delete
- [ ] Node notes can be edited and shown on the canvas (with notes round-trip, see the round-trip ticket)

# Implementation Plan

Render `node.disabled` (opacity plus strike-through), add a toggle in the node toolbar and the Settings tab and bind `D`, and add a node/pane context menu that reuses the existing actions.

# Notes

Related tickets: FEAT-56nep4, FEAT-jvembs

Related (from the audit): FEAT-jvembs and FEAT-56nep4 (both done, with these items unchecked or listed as "Not done"). Pin data: UXD-3.

# Related Files

SD/78-imported-disabled-node.png, SD/disabled.js, SD/ctxmenu.js (`{"nodeContextMenu":[],"paneContextMenu":[]}`). web/src/lib/components/workflow-editor/canvas-node.svelte has no `disabled` handling. shortcuts.ts has no d/p.

# Attachments
