---
id: BUG-15st2k
title: 'Canvas node rendering: AI port labels overlap, placeholders draw 24 ports, branch labels crossed, fallback icons, empty subtitles'
status: todo
priority: high
labels:
    - editor
    - canvas
    - rendering
parent: EPIC-8rbys7
created: "2026-09-23T01:48:15Z"
updated: "2026-09-23T01:48:15Z"
---

# Description

The first impression of an imported n8n workflow is overlapping labels and generic icons. 41 of 45 top templates contain placeholders, and each one draws all 24 AI ports.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: lead, n8n-templates, ux-editor; finding ids: LEAD-8, TPL-5, UXE-17, UXE-25). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## LEAD-8: Canvas rendering defects on an imported AI agent workflow

*ux · medium · editor-canvas*

**Steps to reproduce:**

Import an n8n agent workflow with a chat model, memory and 2 tools; open it. See scratchpad readme/first.png.

**Actual:**

(a) The agent's bottom port labels overlap ("Chat Mode|Tools", "Memory|Output Pa…" collide). (b) The IF node's "true"/"false" output labels are struck through by the outgoing edges. (c) The Telegram (pack) node shows a stray ":" subtitle when resource/operation are unset. (d) The bottom-left zoom controls are partly hidden behind the floating "Chat" button, so the third control is cut off.

**Expected:**

n8n-like layout: evenly spaced sub-node handles with non-overlapping labels, edge labels offset from the line, no empty subtitle, and controls not overlapped.

**Suggested fix:**

Space the ai_* handles by count and truncate labels. Offset output labels. Hide the subtitle when it evaluates empty. Move the chat button or the controls.


## TPL-5: Unsupported placeholders draw all 24 AI ports with overlapping labels, and tool placeholders overlap each other

*ux · high · editor-canvas*

**n8n:** A node type that isn't installed keeps its real shape: a tool is a small round sub-node with one output, and a regular node keeps its own inputs and outputs. The node shows its service name and logo, with a "not installed" state.

**Steps to reproduce:**

1. Import template 1954 "AI agent chat" or 2462 "Angie".
2. Open it in `/app/workflows/<id>`.
3. Look at the SerpAPI, Tasks, Contacts, Google Calendar and Gmail placeholders.

**Actual:**

- Every `kilasflow.unsupported` tile renders 12 AI input diamonds on top, 12 AI output diamonds and 12 "+" buttons underneath, plus main in/out.
- The labels print on top of each other ("inModelinMemoryinToolinEmbedding…"), and the red "Unsupported" pill sits under them.
- The tile is about 3× wider than n8n's sub-node circle, so the 4 agent tools in 2462 overlap into one unreadable strip.
- The icon is a generic "?". The raw type string (`@n8n/n8n-nodes-langchain.toolSerpApi`) is only in the DOM, not visible.
- This affects 41/45 templates.

**Expected:**

A placeholder shows only the ports its edges use, or the ports the original type has. It renders as a sub-node circle when its only connections are AI outputs, and it shows a readable display name derived from the original type (for example "SerpAPI tool — not available").

**Suggested fix:**

Declare, or have the canvas render, only the ports with edges (the importer already computes `observedArity`). Render sub-node shape when there's no main port. Label the tile with a friendly name from `originalType`.

**Evidence:**

- `ui_1954_canvas.png`, `ui_2462_canvas.png`, `ui_2462_unsupported_tools_zoom.png`, `ui_1934_canvas.png`, `ui_2753_canvas.png`
- `nodes/unsupported.go:69,131-143`: every arity declares all AI ports.
- `internal/interop/n8n/n8n.go:1834-1848` (`placeholderFor` picks arity by main edges only).

**Related:**

LEAD-8 in `findings/lead.md` covers label overlap on the agent node itself, not the placeholders.


## UXE-17: Seven node types show the fallback "box" icon, No Operation shows the Unsupported "?" glyph, and many nodes share one glyph

*ux · medium · node-catalog / editor-canvas*

**n8n:** Every node has a distinct icon, and app nodes (Gmail, Google Drive, Telegram, Postgres, OpenAI) use brand logos.

**Steps to reproduce:**

1. Open the picker on an empty workflow and search mail, drive, error and stop. 2. Add a No Operation node.

**Actual:**

Error Trigger, Form, Gmail, Gmail Trigger, Google Drive, Google Drive Trigger and Stop and Error render the fallback cube, which node-visual.ts itself describes as meaning "this editor is older than this node". No Operation uses `circle-help`, the same "?" as the Unsupported placeholder, so it reads as a broken import. IF, Switch and Split Out share one glyph. So do Aggregate, Summarize, Remove Duplicates and Extract From File, and the four chat models and Embeddings. There are no brand logos.

**Expected:**

A distinct glyph per type, and brand artwork for app nodes (the icon route exists).

**Suggested fix:**

Add the five missing lucide glyphs (plus a CI test that every served `builtin:` name maps to a glyph), give noOp an arrow icon, and ship SVGs for the app and pack nodes.

**Evidence:**

SD/04-picker-empty-triggers.png, SD/78-imported-disabled-node.png ("After" = No Operation shows "?"). node-visual.ts:72-104 GLYPHS lacks `alert-triangle`, `form`, `mail`, `folder` and `octagon-alert`, which the catalogue requests (node-types.json).

**Related:**

FEAT-5rvtzc (done: "Serve node icons and remove the hardcoded editor maps")


## UXE-25: Branch wires cross their own port labels, because steps after IF are placed too close

*ux · low · editor-canvas / layout*

**n8n:** Output labels sit clear of the wire, and new nodes after a branch leave room for it.

**Steps to reproduce:**

1. Add IF, then HTTP Request from its "true" "+" handle. 2. Also run Tidy up.

**Actual:**

The wire from "true" runs straight through the word "true", and the "false" "+" stub overlaps the wire. This persists after Tidy.

**Expected:**

Leave extra horizontal gap after nodes with labelled outputs, and route the wire from the handle outward past the label.

**Suggested fix:**

Account for output label width in positionAfter and layout.ts spacing, or draw labels above or below the handle.

**Evidence:**

SD/23-fit-4nodes.png, SD/45-tidy.png.

**Related:**

none


# Acceptance Criteria
- [ ] The agent's AI sub-node handles are spaced by count and their labels never overlap
- [ ] A placeholder renders only the ports its edges use (or its original type's ports), as a sub-node circle when it only has AI outputs, labelled with a friendly name from the original type
- [ ] IF/Switch output labels are offset from the wires, and steps after labelled outputs get extra horizontal gap
- [ ] Every node type has a distinct glyph; No Operation doesn't reuse the Unsupported "?"; app nodes can show brand artwork via the icon route
- [ ] A subtitle that evaluates to empty (":") is hidden

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-5rvtzc

# Related Files

# Attachments
