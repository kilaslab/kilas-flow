---
id: BUG-xam6t8
title: 'AI timeouts: streamed answers die at 30 s, errors blame the wrong bound, and a 1 m default kills agent runs'
status: done
priority: high
labels:
    - ai
    - timeouts
    - debugging
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-09-23T03:22:38Z"
---

# Description

Three bounds (outbound 30 s, execution 1 m, model ceiling 10 m) interact, and the error text names the wrong one. Local and slow models hit them constantly. n8n has no execution timeout by default.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama, lead; finding ids: AI-5, LEAD-3, LEAD-4). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## AI-5: The default model timeout kills streamed local answers at 30 s with "read model stream: context deadline exceeded"

*bug · high · ai-agent*

**n8n:** The OpenAI Chat Model timeout defaults to 60 s per request. The Ollama Chat Model has no short default, and executions have no timeout by default.

**Steps to reproduce:**

1. chatModel (Ollama, `stream: true` is the node default) with no Options → Timeout. Agent prompt "Write a detailed 800-word essay about the history of Java island." (Any agent turn where gemma4 thinks for more than 30 s also triggers it, e.g. the case-4c workflow-tool question.)
2. Run it.

**Actual:**

- `node "AI Agent": model turn 1: read model stream: context deadline exceeded` after 30.2 s. It reproduced 4 times (`case-13-default request timeout streaming`, `case-4c-*` twice, `case-2b-stream-think-1`).
- The error names neither the 30 s bound nor the Timeout option. The non-stream path does ("model request did not answer within 30s (raise the model node's Timeout option…)").
- When the model does fit under the per-request bound, the 1 m execution default kills the run. That message blames "the deployment's 10m0s model timeout ceiling … no node option raises it" although `settings.executionTimeout` raises it (reproduced: `case-13-default execution timeout`, `case-2b-nostream-0`).
- The canvas chat shows these strings verbatim.

**Expected:**

A model-appropriate default (≥ 60 s per request, or none for local endpoints). Every deadline error names the bound that fired and the setting that raises it.

**Suggested fix:**

Wrap stream-read deadline errors with the same "did not answer within X (raise Timeout)" text. Default `chatModel` to 60–300 s, not `outbound.timeout`. Check the parent ctx before blaming the ceiling.

**Evidence:**

`case-13-*.execution.json`, `case-4c-{0,1}.execution.json`, `case11-06-error.png`. Code `nodes/ai.go:1256-1276` (an unset timeout falls back to `outbound.timeout` = 30 s), `internal/ai/openai.go:135` (stream-read error without context), `nodes/ai.go:1396-1414` (misattribution).

**Related:**

BUG-tcqkad (done) claimed that "the deadline error names the option to raise". That holds only on the non-stream path, and stream is the default, so it still reproduces. LEAD-3 and LEAD-4 (this audit) cover the 1 m execution-timeout half.


## LEAD-3: Agent timeout error blames the 10m model ceiling when the 1m execution timeout fired

*bug · high · ai-agent / debugging*

**Steps to reproduce:**

1. An agent on a local Ollama model (gemma4:12b-mlx) with memory and 2 tools, triggered from canvas chat. 2. The model takes over 60s.

**Actual:**

The execution ran exactly 60s (startedAt 01:00:57.5, finishedAt 01:01:57.5, which is `execution.default_timeout: 1m`). The error reads: `the agent did not finish within the deployment's 10m0s model timeout ceiling; that ceiling is a deployment-level bound and no node option raises it: model turn 2: ... context deadline exceeded`.

**Expected:**

The error names the bound that actually fired (the execution timeout, 1m) and the setting that raises it (workflow settings or execution.default_timeout).

**Suggested fix:**

Check `ctx.Err()` (the parent) before attributing to the ceiling, or compare deadlines. Add a test with a parent deadline shorter than the ceiling.

**Evidence:**

nodes/ai.go:1396-1414. `runCtx` derives from the parent ctx, so `errors.Is(runCtx.Err(), DeadlineExceeded)` is also true when the PARENT (execution) deadline expires. The message then prints executor.runTimeout().


## LEAD-4: 1-minute default execution timeout is too short for AI agents; n8n has no default timeout

*ux · medium · ai-agent / engine*

**n8n:** EXECUTIONS_TIMEOUT defaults to -1 (no timeout), and per-workflow timeouts are opt-in.

**Steps to reproduce:**

Any agent run with 2+ tool turns on a local model, or on a slow hosted model under load.

**Actual:**

`execution.default_timeout: 1m0s` kills it, and the canvas chat shows a long raw Go error string.

**Expected:**

A default that fits agent workloads (e.g. 5-10 min, or no timeout for manual/test runs), a per-workflow "Timeout" setting visible in the workflow settings UI, and a friendly message.

**Suggested fix:**

Raise the default or exempt manual runs. Expose the timeout in the workflow settings dialog and link to it from the error.


# Acceptance Criteria
- [x] A model-appropriate default for streamed requests (at least 60 s per request, or none for local endpoints), with the outbound 30 s not applied silently to model streams
- [x] Every deadline error names the bound that actually fired (the parent context's deadline is checked before blaming the model ceiling) and the setting that raises it
- [x] The default execution timeout fits agent workloads, or manual/chat test runs are exempt
- [x] A test with a parent deadline shorter than the ceiling asserts the right attribution

# Implementation Plan

See each finding's suggested fix above.

# Notes

**2026-09-23: partly fixed.**
- **Streamed requests are bounded by silence, not length.** `internal/ai/openai.go` `idleDeadline` resets the per-request timeout on every chunk, and still bounds the wait for the first byte. gemma4 on Ollama streams its reasoning for about 80 s before its answer, and was cut at 30 s before this. A silent stream fails with "model stream was silent for 30s (raise the model node's Timeout option…)". Two tests in `openai_test.go` cover it.
- **Attribution.** `nodes/ai.go` checks the caller's context before blaming the model ceiling. An agent ended by the execution's own time limit now says so, and names `executionTimeout` and `execution.default_timeout`. Covered by `TestARunEndedByTheExecutionTimeoutDoesNotBlameTheModelCeiling`.
- **Owner decision, implemented the same day:** "default 2 minutes, can be configured". `execution.default_timeout` now defaults to `2m`, set in `internal/config/config.go` and covered by `TestExecutionDefaultTimeoutIsTwoMinutesAndConfigurable`, which also proves the environment override. `config.example.yaml`, the configuration reference, the concept pages and the CHANGELOG are updated. The per-workflow `executionTimeout` still wins; its UI is FEAT-fpqg78.

Related tickets: BUG-tcqkad

The per-workflow `settings.executionTimeout` already exists in the engine; its UI is FEAT-fpqg78.

# Related Files

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Files changed (base → working tree):

```
 .pine/memory/code-node.md                          |   3 +-
 CHANGELOG.md                                       |  25 ++
 CONTRIBUTING.md                                    |  22 +
 README.md                                          | 450 ++++++---------------
 config.example.yaml                                |   8 +-
 docs/src/content/docs/concepts/architecture.md     |  84 ++++
 docs/src/content/docs/concepts/execution-model.md  |  15 +-
 docs/src/content/docs/concepts/node-registry.md    |   5 +-
 .../src/content/docs/concepts/safety-boundaries.md |   2 +-
 docs/src/content/docs/concepts/webhooks.md         |  33 ++
 .../content/docs/operate/acceptance-capstone.md    |   3 +-
 .../docs/operate/configuration-reference.md        |   8 +-
 docs/src/content/docs/reference/api-contract.md    |  15 +-
 docs/src/content/docs/reference/api.md             |   2 +-
 docs/src/content/docs/reference/api/events.md      |  13 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |  15 +-
 e2e/helpers/seed.ts                                |  15 +
 e2e/tests/editor-chat.spec.ts                      |  68 +++-
 internal/ai/openai.go                              |  77 +++-
 internal/ai/openai_test.go                         |  66 +++
 internal/api/handlers/executions.go                |  46 ++-
 internal/config/config.go                          |  10 +-
 internal/config/config_test.go                     |  22 +
 internal/engine/approval.go                        |   2 +-
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
 39 files changed, 1717 insertions(+), 496 deletions(-)
```
