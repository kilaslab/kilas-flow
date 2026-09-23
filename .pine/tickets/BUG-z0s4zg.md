---
id: BUG-z0s4zg
title: ai.* and webhook.response SSE events are unnamed, and each prints a goroutine dump to the server log
status: done
priority: medium
labels:
    - events
    - observability
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T03:11:06Z"
---

# Description

EventSource clients listen by event name, so they never see agent events. Meanwhile the server log fills with stack traces that bury real errors.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-18). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n/a (push events are typed).

# Steps to Reproduce

1. Run any AI agent workflow, for example exec_01a0cbc4-134f-…. 2. `kilasflow exec trace <id>`. 3. `grep -c "unknown event type handlers.ExecutionEvent" $SP/kf-server.log`.

# Expected

Every published type registered in `executionEventSchemas`/`typedEvent`, or unknown types folded into a generic named event without the huma stderr trace.

# Actual

In the trace, all 56 agent events have `"event": ""` (for example `{"id":9,"event":"","data":{"type":"ai.model.started",…}}`), so EventSource clients, which listen by name, never see them. The server log holds 56 copies of `error: unknown event type handlers.ExecutionEvent` followed by a full goroutine dump from huma `sse.go:267`, which buries real errors.

# Acceptance Criteria
- [x] Every published event type (ai.*, webhook.response, and any `NodeEvent` name) is registered in `executionEventSchemas`/`typedEvent`
- [x] Unknown types fall back to a generic named event, never a huma stderr trace
- [x] A test asserts that every event name emitted anywhere is registered

# Implementation Plan

Add the ai.* and webhook.response types to both maps, and add a test that every `NodeEvent` name emitted anywhere is registered.

# Notes

**2026-09-23: fixed.**
- `internal/api/handlers/executions.go` registers `webhook.response`, the eight `ai.*` kinds and a named fallback, `execution.event` (`OtherEvent`). The fallback carries the real name in the payload's `type`. Nothing goes out unnamed, and huma no longer prints a trace.
- `executions_events_test.go` asserts that every engine, AI and webhook event constant has a schema entry whose Go type matches `typedEvent`. It also asserts that an unknown name falls back to the named frame.
- The web client listens for all of them through one shared `EXECUTION_EVENT_NAMES` list in `event-stream.svelte.ts`.
- The events reference page is regenerated, including the previously missing `execution.waiting`.
- The generated web client and SDK types are regenerated.

Related tickets: BUG-qmgz2f, BUG-y57cz4

Related (from the audit): BUG-y57cz4 (done; the same class of defect for `execution.failed`), BUG-qmgz2f ("ai.* drops" in its title was closed as "nothing found")

# Related Files

`$SP/agents/ux-debug/cli/trace_ai.json`, `$SP/kf-server.log` lines 393-455 (first trace). internal/api/handlers/executions.go:64-117 (no case for `ai.*` from internal/ai/ai.go:170-177 or `webhook.response` from internal/engine/runner.go:95). The engine publishes arbitrary `events.Type(event.Name)` at internal/engine/wait_service.go:447-451.

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
