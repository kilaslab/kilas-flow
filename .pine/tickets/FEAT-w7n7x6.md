---
id: FEAT-w7n7x6
title: 'Node placement: new nodes stack at the viewport centre, Tab ignores selection, ''+'' leaves source selected, delete doesn''t reconnect'
status: todo
priority: medium
labels:
    - editor
    - canvas
    - layout
parent: EPIC-8rbys7
created: "2026-09-23T01:48:14Z"
updated: "2026-09-23T01:48:14Z"
---

# Description

Building a chain by pressing Tab and "+" should flow left to right like n8n. Instead nodes pile up, hide behind the inspector, or leave gaps.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-5, UXE-6, UXE-23). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## UXE-5: New nodes land on top of existing ones, and Tab with a node selected does not connect to it

*ux · medium · editor-canvas / node placement*

**n8n:** A node added while another is selected is connected to it and placed to its right. Otherwise it goes to a free spot, and pasted nodes land at the cursor.

**Steps to reproduce:**

1. In a workflow Manual Trigger → Wait → HTTP Request, click HTTP Request and press Tab. 2. Pick "Limit". 3. Separately: click empty canvas and add nodes with Tab three times, paste twice, and add a Sticky Note.

**Actual:**

Limit is added **unconnected** at (574,398), right on top of Manual Trigger (582,398). Nodes added with Tab or N go to the exact viewport centre every time, so Embeddings / Data table Tool / Merge stacked on identical coordinates (422,398 / 502,398 / 502,398). Pasted copies and new stickies also overlap existing nodes, and a sticky dropped this way covers the nodes it lands on.

**Expected:**

Connect the new node to the selected node and place it after it (positionAfter already exists). Otherwise, pick a free slot.

**Suggested fix:**

When exactly one node is selected, have Tab/N/Add step call `openPickerFrom(selected, firstOutput)`. Run viewportPlacement and paste positions through the collision check positionAfter uses.

**Evidence:**

SD/10-canvas-after-adds.png, SD/76-tab-with-selection.png, SD/36-after-paste.png, SD/40-sticky.png. Code: workflow-editor.svelte:730-735 `viewportPlacement` ignores `index` and existing nodes. `case 'add-step'` (926) calls `openPicker(false)`, which clears `pendingSource` (424-431).

**Related:**

FEAT-jvembs ("New and imported nodes are placed without regard to the viewport or existing nodes", unchecked)


## UXE-6: Adding a step from a "+" handle leaves the source node selected, and the new node lands hidden behind the inspector

*bug · medium · editor-canvas / selection*

**n8n:** The new node is selected, its NDV opens, and the canvas keeps it in view.

**Steps to reproduce:**

1. Click the "+" after Manual Trigger and choose Set (Enter). 2. Repeat from Set → IF (mouse click) and from IF true → HTTP Request.

**Actual:**

All 3 times the inspector kept showing the **source** node (Manual Trigger, then Set, then IF), and `.selected` stayed on the source. At the common 1.5-2x zoom the new node is placed under the 320 px inspector, where it cannot be seen. Selecting a node that sits under the inspector does not pan it into view either. Pressing `1` fits to 200% on small workflows (the toolbar fit button caps at 100%).

**Expected:**

The new node is selected and its inspector opens. The viewport pans so the node is visible beside the panel.

**Suggested fix:**

After addNode, set Svelte Flow's selection explicitly (update nodes with selected flags in the same tick) and call setCenter/fitBounds on the new node, offset for the inspector width. Share one fitView option set.

**Evidence:**

SD/11-set-added.png, SD/12-if-added.png, SD/22-http-added.png, SD/21-if-filled.png. addNode sets `selectedNodeIDs = [node.id]` (workflow-editor.svelte:532), but the canvas's own selection event (onSelectionChange, 589) restores the old selection. There is no pan when a source exists (542). editor-controls.svelte:32 caps fit at maxZoom 1, and the `1` shortcut (936) does not.

**Related:**

none


## UXE-23: Deleting a node in the middle of a chain does not reconnect its neighbours

*gap · low · editor-canvas*

**n8n:** Deleting a node with one input and one output reconnects its predecessor to its successor.

**Steps to reproduce:**

1. In Manual Trigger → Wait → HTTP Request, select Wait and press Delete.

**Actual:**

Both edges disappear, leaving Manual Trigger and HTTP Request disconnected. Cmd+Z restores them.

**Expected:**

Manual Trigger → HTTP Request is reconnected.

**Suggested fix:**

In onDelete, when a removed node had exactly one incoming and one outgoing main connection, add source→target (if canConnect allows it).

**Evidence:**

SD/delmid.js output (`edges: []`).

**Related:**

none


# Acceptance Criteria
- [ ] Tab with a node selected connects the new node to it and places it after it (`positionAfter`); otherwise the new node goes in a free slot
- [ ] Adding from "+" selects the new node, opens its inspector, and pans it into view beside the panel
- [ ] Deleting a middle node reconnects its single predecessor and successor

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-jvembs

# Related Files

# Attachments
