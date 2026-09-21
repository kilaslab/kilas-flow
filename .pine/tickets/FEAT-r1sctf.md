---
id: FEAT-r1sctf
title: Chat Trigger and editor test chat
status: done
priority: medium
created: "2026-09-21T10:45:30Z"
updated: "2026-09-21T11:26:05Z"
---

# Description

Editor-only Chat Trigger (`kilasflow.chatTrigger`) plus a canvas Chat panel. One message queues one manual run with `{action, sessionId, chatInput}`. Hosted/embed chat is out of scope.

# Acceptance Criteria
- [x] `kilasflow.chatTrigger` is registered with executor `core.chatTrigger` and emits the run input
- [x] Compiler refuses a second chat trigger (`workflow.invalid_topology`)
- [x] New Agent nodes default prompt to the expression marker `{{ $json.chatInput }}`
- [x] Execute never fires a chat trigger with `{}`; chat-only graphs open the Chat panel
- [x] Canvas Chat panel uses a stable sessionId, polls `GET /executions/{id}`, walk-back `output` then `text`
- [x] n8n `@n8n/n8n-nodes-langchain.chatTrigger` 1.x imports; hosted/public dropped; export `public: false`

# Implementation Plan

See the attached Editor Chat Trigger plan. Do not implement hosted chat, embed widgets, streaming, or `execution.Trigger = chat`.

# Notes

- Execute with Chat+Manual always sends the non-chat `triggerNodeId`. Omitting it would still run the chat trigger with an empty item.
- Reply extraction walks succeeded node runs backwards rather than trusting the last node, matching n8n's documented last-node failure when the chain ends on Set/HTTP/datastore.
- Agent prompt `Default` is copied by `createWorkflowNode` for new nodes; imported/saved prompts are unchanged.

# Related Files

- nodes/chat.go
- web/src/lib/workflow-editor/chat.ts
- web/src/lib/components/workflow-editor/canvas-chat-panel.svelte
- internal/interop/n8n/n8n.go

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `53a6f765` (last commit at or before ticket created 2026-09-21)
- Files changed (base → working tree):

```
 .pine/memory/web-editor.md                         |  3 +-
 e2e/helpers/seed.ts                                | 35 ++++++++++
 e2e/tests/node-coverage.spec.ts                    | 26 ++++++++
 internal/interop/n8n/n8n.go                        | 12 ++++
 internal/interop/n8n/n8n_test.go                   | 57 ++++++++++++++++
 internal/interop/n8n/parameters.go                 | 32 +++++++++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |  7 ++
 internal/workflow/compiler.go                      | 23 +++++++
 internal/workflow/document_test.go                 | 49 ++++++++++++++
 nodes/ai.go                                        |  6 ++
 nodes/core.go                                      |  1 +
 nodes/executors.go                                 |  1 +
 nodes/presentation_test.go                         |  2 +-
 skills/index.json                                  |  3 +-
 skills/kilasflow-triggers/SKILL.md                 |  6 +-
 web/messages/en/editor.json                        | 11 ++++
 web/messages/id/editor.json                        | 11 ++++
 .../workflow-editor/workflow-editor.svelte         | 77 +++++++++++++++++-----
 web/src/lib/embed/embed-editor.svelte              | 20 ++++--
 web/src/lib/workflow-editor/document.test.ts       | 19 ++++++
 web/src/lib/workflow-editor/run-trigger.test.ts    | 37 ++++++++++-
 web/src/lib/workflow-editor/run-trigger.ts         | 29 ++++++++
 .../(dashboard)/app/workflows/[id]/+page.svelte    | 21 ++++--
 23 files changed, 451 insertions(+), 37 deletions(-)
```
