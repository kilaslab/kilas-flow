---
id: BUG-15st2k
title: 'Canvas node rendering: AI port labels overlap, placeholders draw 24 ports, branch labels crossed, fallback icons, empty subtitles'
status: done
priority: high
labels:
    - editor
    - canvas
    - rendering
parent: EPIC-8rbys7
created: "2026-09-23T01:48:15Z"
updated: "2026-10-08T16:20:00Z"
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
- [x] The agent's AI sub-node handles are spaced by count and their labels never overlap
- [x] A placeholder renders only the ports its edges use (or its original type's ports), as a sub-node circle when it only has AI outputs, labelled with a friendly name from the original type
- [x] IF/Switch output labels are offset from the wires, and steps after labelled outputs get extra horizontal gap
- [x] Every node type has a distinct glyph; No Operation doesn't reuse the Unsupported "?"; app nodes can show brand artwork via the icon route
- [x] A subtitle that evaluates to empty (":") is hidden

## Verification (2026-10-08): still open

Fix commit: `690a2da`. Three of the five criteria are met. Two are not: the placeholder still draws an unused port, and glyphs are not distinct per type.

- **AI sub-node handles spaced, labels never overlap (met).** `attachmentLabelRows` (`web/src/lib/workflow-editor/node-visual.ts:221`) spreads the labels over as many rows as the spacing needs (`canvas-node.svelte:111`). Tests: `node-visual.test.ts` `describe('attachmentLabelRows')`. In the app, an AI Agent with a chat model, a memory and two tools shows "Chat M…" and "Tools" on row 1 and "Memory" and "Output…" on row 2, with no overlap, and the fill buttons sit below the label rows (`02-agent-ports-zoom.png`). The dashed edges still run vertically through the labels, which is cosmetic only.
- **Placeholder (NOT met).** The placeholder draws only the ports that have edges (`canvas-node.svelte:69`), renders as a sub-node circle, and shows the friendly subtitle "Tool Serp Api" (`friendlyOriginalType`, `node-visual.ts:237`). However, the fallback runs separately for each side. When no input is wired, `keep()` puts back the first main input even though the placeholder's only edge is an AI output. So an imported SerpAPI tool still draws a main input handle on its left that no edge uses (`02-agent-ports-zoom.png`). Fix: fall back to the plain main in/out only when the placeholder has no wired port at all, and add a test (pull the port selection out of `canvas-node.svelte` into a pure helper).
- **IF/Switch labels offset from wires, extra gap after labelled outputs (met).** Output labels sit above the wire (`canvas-node.svelte:296`). `positionAfter` uses `BRANCH_COLUMN` (`document.ts:57`) after a `hasLabelledOutputs` node (`ports.ts:76`), and Tidy adds `extraWidth` (`layout.ts:59,100`). In the app, "Success"/"Error" and "true"/"false" sit above their wires (`07-callapi-settings-onerror.png`). This verification also applied the same offset to the execution replay node (`execution-canvas-node.svelte`), where "done"/"loop" and "Success"/"Error" were still centred on the wire and read as struck through (`13-loop-tag-run-selector.png` shows the fixed replay).
- **Distinct glyph per type (NOT met).** Done: the fallback cube is gone. `glyphs.json` is checked from both sides by `nodes/glyphs_test.go` `TestEveryBuiltinIconNameIsShippedInGlyphs` and `node-visual.test.ts` "shipped glyphs". No Operation now uses `builtin:arrow-right` (`nodes/flow.go:364`) instead of the Unsupported "?". Not done: the served catalogue (`GET /api/v1/node-types`, 62 types) still has several types sharing one glyph:
  - `git-branch`: IF, Switch, Split Out
  - `archive`: Aggregate, Summarize, Remove Duplicates, Extract From File
  - `brain`: OpenAI Chat Model, OpenAI-Compatible Chat Model, OpenRouter Chat Model, Embeddings
  - `database`: MySQL, PostgreSQL, SQLite, Postgres PGVector Store
  - `message-circle`: When chat message received, WAHA, WAHA Trigger
  - `scissors`: Limit, Recursive Character Text Splitter
  - `arrow-down-up`: Sort, Structured Output Parser
  - `layers`: Default Data Loader, Vector Store

  A node sharing a glyph with its own trigger or tool variant (Gmail and Gmail Trigger, HTTP Request and HTTP Request Tool) is arguably fine. No brand artwork is shipped for the app nodes (Gmail, Google Drive, Telegram). The icon route from FEAT-5rvtzc exists but no definition uses it.
- **Empty subtitle hidden (met).** `nodeSubtitle` strips separator runs at either end before it decides the line is empty (`node-visual.ts`). This verification added the test `node-visual.test.ts` "hides a line that renders only its separators". The Chat button moved to the bottom-right (`workflow-editor.svelte:1333`), so it no longer covers the zoom controls (LEAD-8 d, confirmed in the app).

Self-test setup: `make build-web` + `go build ./cmd/kilasflow` from this branch, served on 127.0.0.1:18095 with an isolated SQLite file (`KILASFLOW_DATABASE_DSN=/tmp/kf-verify/data/kilasflow.db`), driven with playwright-cli. Probe workflows were imported through `POST /api/v1/workflows/import`. Screenshots are in `/tmp/kf-verify/` and are not committed.

## Fix (2026-10-08): the two open gaps

- **Placeholder ports.** The port selection moved out of `canvas-node.svelte` into the pure helper `placeholderDefinition(definition, wiredInputs, wiredOutputs)` (`web/src/lib/workflow-editor/node-visual.ts`). It matches inputs against target handles and outputs against source handles separately, because both sides have a port called `main`. The old code merged them into one set, so a wired main output also kept the main input. It falls back to the plain main in and out only when nothing is wired at all. A tool wired only by its AI output now has no inputs, so it draws as the attachment circle. The execution replay node (`execution-canvas-node.svelte`) never trimmed placeholder ports and drew all 24. It now uses the same helper and shows the same friendly subtitle. Tests: `node-visual.test.ts` `describe('placeholderDefinition')` (5 cases).
- **One glyph per type.** New test `nodes/glyphs_test.go` `TestEveryNodeTypeHasAGlyphOfItsOwn` takes every type `RegisterAll`, `RegisterVectorNodes` and the Telegram and WAHA packs register. It resolves each icon name through `glyphs.json` to its lucide component, and fails when two types draw the same component. Comparing components rather than names catches aliases: `memory-stick` used to resolve to `Archive`, the same icon as Aggregate. The only allowed exceptions are listed in `glyphVariants`: a trigger or tool that is the same service as its base node (Gmail Trigger, Google Drive Trigger, Telegram Trigger and pack.telegram, WAHA Trigger, HTTP Request Tool, Calculator Tool, Data table Tool, Workflow Tool and the Execute Workflow trigger). Different versions of one type share a glyph by design. A frontend test (`node-visual.test.ts` "draws two names alike only when glyphs.json says they are the same icon") keeps `GLYPHS` from aliasing two names that `glyphs.json` keeps apart. The new assignments:
  - IF keeps `git-branch` (Split). Switch → `signpost`. Split Out → `ungroup`.
  - Aggregate → `group`. Summarize → `sigma`. Remove Duplicates → `copy-minus`. Extract From File → `file-output`. Simple Memory's `memory-stick` now draws `MemoryStick`, not `Archive`.
  - OpenAI Chat Model → `sparkles`. OpenAI-Compatible Chat Model → `brain`, which now draws `Brain`, not `Sparkles`. OpenRouter Chat Model → `route`. Embeddings → `axis-3d`. The provider's glyph is a new `icon` field on `chatModelProvider`.
  - PostgreSQL v1/v2 keep `database`. MySQL v1/v2 → `server`. SQLite → `hard-drive`. `databaseNode` takes the icon as a parameter. Postgres PGVector Store → `database-search`. Vector Store → `boxes`. Default Data Loader keeps `layers`.
  - When chat message received → `messages-square` (WAHA keeps `message-circle`). Limit → `list-end`. Text splitter keeps `scissors`. Structured Output Parser → `braces`. Sort keeps `arrow-down-up`.
  - Code keeps `code`. Code (JavaScript) → `code-xml`. Code (JavaScript or Python) → `square-terminal`.
- **Brand artwork (not done, out of scope).** App nodes can show artwork: a definition carries an `IconAsset`, and `GET /api/v1/node-types/{type}/icon` serves it as an `<img>` (`servedIconURL`). No built-in app node ships brand SVGs yet (Gmail, Google Drive, Telegram, WAHA, OpenAI, OpenRouter, PostgreSQL, MySQL). They still draw the distinct lucide glyphs above. Shipping the artwork, with licensing checked, is follow-up work.
- **Self-test.** Ran `make build-web` and `go build`, then served on 127.0.0.1:18096 with an isolated SQLite file and drove it with playwright-cli. The probe workflow had a chat trigger, an AI Agent with an OpenAI model and a memory, and two tool placeholders (SerpAPI and Google Calendar Tool), then IF → Switch / Split Out. It was imported with `POST /api/v1/workflows/import`. Each placeholder rendered one handle, `top:outTool`, and no main input. Both drew as circles subtitled "Tool Serp Api" and "Google Calendar Tool". IF, Switch and Split Out drew three different glyphs. Screenshot: `/tmp/kf-ui3/01-canvas.png` (not committed). The replay node was not exercised in the app: a workflow that contains a placeholder cannot run, so there is no execution to replay. It shares the tested helper.
- **Checks.** `go test ./nodes/ ./packs/... ./internal/node/` ok. `pnpm vitest run src/lib/workflow-editor` 514/514. `pnpm check` 0 errors, 0 warnings.

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-5rvtzc

# Related Files

# Attachments
