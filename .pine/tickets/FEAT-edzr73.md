---
id: FEAT-edzr73
title: 'Canvas chat panel: markdown, preserved newlines, token streaming, visible tool steps, human-worded errors'
status: done
priority: high
labels:
    - ai
    - editor-chat
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-09-23T03:11:06Z"
---

# Description

Memory and "New chat" work, but a reply shows no markdown, newlines collapse into one line, tokens don't stream, and tool calls are hidden. Errors appear as raw Go strings.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama; finding ids: AI-9). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The chat pane renders markdown, streams tokens when the agent has streaming on, and the logs panel beside it shows every model and tool call, with inputs, outputs, tokens and duration, per message.

# Steps to Reproduce

1. Open `[ai-ollama] case11 canvas chat agent` (Chat Trigger → Agent with memory, Calculator, `enableStreaming: true`).
2. Click Chat and send "What is 1234*5678 minus 91? Then list three fruits."

# Expected

Markdown (or at least `whitespace-pre-wrap`), token streaming from the execution SSE when streaming is on, a collapsible "used Calculator(…) → 7006561" step per reply linked to the execution, human-worded errors, and the memory notice only when a memory node is attached.

# Actual

- The bubble reads "…is 7,006,561. Here are three fruits: * Apple * Banana * Orange" on one line. The stored output is `…\n\nHere are three fruits:\n* Apple\n* Banana\n* Orange`, but the bubble has `white-space: normal` and renders no markdown.
- During the run the panel shows only "Running…". No tokens stream, although `ai.model.delta` events are emitted.
- Nothing shows that the Calculator was called.
- Errors appear as raw Go strings (`execute node "agent": node "AI Agent": model turn 1: read model stream: context deadline exceeded`).
- The "Simple Memory is in-process…" notice shows even on a workflow with no memory node.
- The execution page lists the chat run's Trigger as "Manual".
- Memory across turns and "New chat" (new sessionId) both worked.

# Acceptance Criteria
- [x] Replies render markdown (or at least `whitespace-pre-wrap`)
- [x] Tokens stream from the execution SSE when streaming is on
- [x] Each reply has a collapsible "used Calculator(…) → 7006561" step list, linked to the execution
- [x] Errors are human-worded, with a link to the execution
- [x] The in-process memory notice appears only when Simple Memory is actually attached

# Implementation Plan

Render with the existing markdown component plus `whitespace-pre-wrap`. Subscribe to `/executions/{id}/events` for `ai.model.delta` and `ai.tool.*`. Show tool steps inline. Gate the memory notice on an attached memory node.

# Notes

**2026-09-23: done. Verified end to end in a browser against local Ollama (gemma4) and in `e2e/tests/editor-chat.spec.ts`, 4/4.**
- **Markdown.** `chat-markdown.ts` is a safe parser covering paragraphs, breaks, lists, code, links (http/https/mailto only), headings and quotes. `chat-markdown.svelte` renders it with elements only, never `{@html}`, so markup in a reply is inert text. 14 unit tests.
- **Streaming.** `chat-stream.ts` folds the `ai.*` events into the text being written plus live tool steps. 8 unit tests. `execution-watch.ts` opens the execution's event stream, and the page's run loop resolves on the terminal event instead of waiting up to a 1 s poll.
- **Tool steps.** A collapsible step list per reply shows input and result. Each reply has its duration and a "View execution" link.
- **Errors.** `chatReplyFromExecution` names the failed node once and lifts the provider's sentence out of its JSON body. A 422 lists every blocking issue with node names, and clicking an issue opens that node.
- **Memory notice.** It appears only when `chatHasMemory` finds a wired memory sub-node.
- **Also added:**
  - unsaved edits are saved before sending (a notice explains this, and the input stays enabled);
  - the input is focused on open and after each reply;
  - an expand toggle, and Esc closes the panel;
  - an elapsed-seconds counter while the model thinks;
  - a Shift+Enter hint.
- **Server fixes this needed:**
  - BUG-z0s4zg: the SSE event names.
  - The chat model's `stream` now defaults to the definition's `true` when the key is absent. Before, untouched and imported nodes never streamed: AI-11 in BUG-nzy3pa.
  - Streamed model requests are bounded by silence, not total length (BUG-xam6t8).
- **Editor bug found and fixed on the way:** clicking any validation issue selected its node, and Svelte Flow's stale selection report deselected it again immediately. The editor's own banner issues never opened the node either. Fixed with `pendingSelection` in `workflow-editor.svelte`.

Related tickets: FEAT-r1sctf

Related (from the audit): FEAT-r1sctf (done; streaming explicitly out of scope). UXD-17 (logs view), LEAD-2 (chat errors).

# Related Files

`case11-04-reply.png`, `case11-03-waiting.png`, `case11-06-error.png`, `case11-09-execution-page.png`, `case-11-ui-1.execution.json`. Code `web/src/lib/components/workflow-editor/canvas-chat-panel.svelte:120-127` (plain `{message.text}` in a `text-xs leading-5` div).

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Files changed (base → working tree):

```
 CONTRIBUTING.md                                    |  22 +
 README.md                                          | 450 ++++++---------------
 docs/src/content/docs/concepts/architecture.md     |  84 ++++
 docs/src/content/docs/concepts/node-registry.md    |   5 +-
 docs/src/content/docs/concepts/webhooks.md         |  33 ++
 .../content/docs/operate/acceptance-capstone.md    |   3 +-
 docs/src/content/docs/reference/api.md             |   2 +-
 docs/src/content/docs/reference/api/events.md      |  13 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |  15 +-
 e2e/helpers/seed.ts                                |  15 +
 e2e/tests/editor-chat.spec.ts                      |  68 +++-
 internal/ai/openai.go                              |  77 +++-
 internal/ai/openai_test.go                         |  66 +++
 internal/api/handlers/executions.go                |  46 ++-
 nodes/ai.go                                        |  75 ++--
 nodes/ai_test.go                                   |  59 +++
 scripts/generate-api-reference.mjs                 |  28 +-
 sdk/src/generated/models.ts                        | 250 ++++++++++++
 web/messages/en/editor.json                        |  20 +-
 web/messages/id/editor.json                        |  20 +-
 web/src/lib/api/generated/models/index.ts          |  10 +
 .../models/streamExecutionEvents200Item.ts         |  90 +++++
 .../workflow-editor/canvas-chat-panel.svelte       | 381 ++++++++++++++---
 .../workflow-editor/workflow-editor.svelte         |  54 ++-
 web/src/lib/workflow-editor/chat.test.ts           |  54 ++-
 web/src/lib/workflow-editor/chat.ts                |  78 +++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |  44 +-
 web/src/lib/workflow-editor/validation.ts          |   5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  36 +-
 29 files changed, 1627 insertions(+), 476 deletions(-)
```
