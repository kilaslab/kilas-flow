---
id: FEAT-az620p
title: Reach parity on the workflow-composition node family
status: todo
priority: medium
labels:
    - nodes
    - parity
deps:
    - FEAT-a6yg3n
    - FEAT-9knk67
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T05:02:49Z"
updated: "2026-09-05T05:02:49Z"
---

## Scope

KilasFlow cannot call a workflow from a workflow. There is no Execute Workflow node, no Execute Sub-workflow Trigger, and nothing in the engine that would let one exist: `engine.Request` in `internal/engine/runner.go` carries `Input`, `Execution`, `Env`, `Credentials`, `Events` and `NodeOutputs`, and none of those is a handle an executor could use to start another run. `engine.Service` in `internal/engine/service.go` exposes `QueueWebhook` and `QueueScheduled` and nothing else, and `execution.Trigger` in `internal/execution/records.go` has exactly three values — `manual`, `webhook`, `schedule`. `Runner.Run` says what it does in its own doc comment: it "executes every node of one compiled graph exactly once". Every n8n workflow in the corpus that factors shared logic into a sub-workflow imports as `kilasflow.unsupported` and cannot be activated.

The response side is subtler and worse, because it looks like it works. `respondFromExecution` in `internal/webhook/webhook.go` honours only `responseNode`; for `lastNode` and for `immediate` it writes a KilasFlow envelope, `{executionId, status, data: outputs}`, where `outputs` is every terminal node keyed by node ID. n8n's `lastNode` returns the last node's items as the body. So a webhook workflow imported with `responseMode: "lastNode"` — a common shape — activates, runs, returns 200, and gives the caller the wrong body with no error anywhere. The Respond to Webhook node itself only has `responseCode`, `responseBody` and `responseHeaders` (`respondToWebhookNode` in `nodes/webhook.go`), and `respondToKilas` in `internal/interop/n8n/parameters.go` names the gap for `respondWith` values other than `text` and `json` — n8n also has `binary`, `redirect`, `noData`, `allIncomingItems`, `firstIncomingItem` and `jwt`.

This ticket adds sub-workflow composition and makes the webhook response modes mean what n8n means. The related export bug — `webhookToN8N` writing KilasFlow's `immediate` into n8n's `responseMode`, which is not a value in n8n's enum — belongs to p1-12; this ticket must keep the mode names consistent with whatever p1-12 settles.

## Acceptance criteria

- [ ] An Execute Workflow node runs another workflow of the same tenant and returns its output items, with a mode for "wait for completion" and one for "fire and forget".
- [ ] An Execute Sub-workflow Trigger is a registered trigger that accepts a defined input schema, and a workflow starting from it compiles and runs.
- [ ] A sub-workflow call records its own execution with the parent execution ID on it, so the chain is visible in execution history and in the events stream.
- [ ] Recursion is refused, not survived: a call whose stack already contains the target workflow, or that exceeds a configured depth, fails with a message naming the cycle, and a test proves a self-calling workflow cannot exhaust the worker pool.
- [ ] A sub-workflow can never reach another tenant's workflows or credentials, proven by a test that names a workflow ID from a second tenant and is refused.
- [ ] Webhook `lastNode` mode returns the last executed node's items as the response body, matching n8n, and `immediate` returns as soon as the run is queued.
- [ ] Respond to Webhook supports n8n's `respondWith` set — text, JSON, all incoming items, first incoming item, no data and redirect — and refuses binary and JWT with a named diagnostic.
- [ ] `internal/interop/n8n` maps `n8n-nodes-base.executeWorkflow` and `.executeWorkflowTrigger` in both directions and `SupportedMappings()` lists them.

## Implementation Plan

The invoker is the design decision, and it has one right answer. Give `engine.Request` a workflow invoker interface, implemented by `engine.Service`, and run the child graph **inline in the calling worker's goroutine**: compile the child, run it with a request derived from the parent's, and return its terminal items. The alternative — writing a child execution record and letting the worker pool pick it up while the parent blocks — deadlocks as soon as the call depth exceeds the pool size, and the pool is `cfg.Execution.MaxConcurrent`, which defaults to 10 (`internal/config/config.go`, used at `cmd/kilasflow/main.go`). Ten nested calls would hang the whole install. Persist a child execution record for evidence, but do not schedule it.

Carry the call stack, not just a depth counter, on `ExecutionContext`: a slice of workflow IDs plus the parent execution ID. A depth limit alone lets A→B→A→B run to the limit and waste the budget; the stack refuses the second A immediately and can name the cycle in the error. Add `execution.TriggerSubworkflow` to `internal/execution/records.go` so the child's provenance is not a lie, and set the parent execution ID on the child record.

The compiler needs the sub-workflow trigger admitted as a root kind. `internal/workflow/compiler.go` requires `len(roots) != 1` to fail, which p1-3 is already relaxing for multiple triggers; the sub-workflow trigger simply has to be recognised as a root by the same code rather than a second rule bolted on.

Then the response modes, in `respondFromExecution` in `internal/webhook/webhook.go`. `lastNode` needs the last node that actually ran, which the engine already knows — `Result.NodeRuns` is appended in execution order — but `record.Output` only carries terminal nodes keyed by node ID, so the information is lost by the time the webhook handler reads it. Persist the responding node's identity in the execution record rather than reconstructing it from the output map, and note that `findResponse` iterating a Go map is what makes two Respond nodes on opposite branches nondeterministic; that determinism is p1-1's, and this ticket must not paper over it with a sort.

The trap is the embed boundary. `ownsExecution` in `internal/api/handlers/executions.go` confines an embed session to executions of its own workflow, and a sub-workflow execution belongs to a different workflow — so an embedded editor would show the parent succeeding with an invisible child. Decide explicitly: recommend allowing an embed session to read a child execution whose ancestry reaches its own workflow, since the alternative is a chain the user can start but not inspect.

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section p4 (workflow composition); p1-1 branch pruning and Respond determinism; p1-3 multiple trigger roots; p1-12 import diagnostics and the `responseMode` export enum.
- PRD `gflow-prd-v1.md` §35 Workflow Execution Model, §36 API, §24 Native V1 Nodes.
- Verified in this repository: `internal/engine/runner.go` (`Request`, `Runner.Run`), `internal/engine/service.go` (`run`, `QueueWebhook`, `QueueScheduled`, `tenantCredentials`), `internal/execution/records.go` (`Trigger` values), `internal/workflow/compiler.go` (`len(roots) != 1`), `internal/webhook/webhook.go` (`respondFromExecution`, `findResponse`), `nodes/webhook.go` (`respondToWebhookNode`, response-mode constants), `internal/interop/n8n/parameters.go` (`respondToKilas`, `webhookToN8N`), `internal/api/handlers/executions.go` (`ownsExecution`), `internal/config/config.go` (`MaxConcurrent` default 10).
- n8n 2.34.0 reference checkout (read-only, outside this repo): the type strings `n8n-nodes-base.executeWorkflow` and `n8n-nodes-base.executeWorkflowTrigger` are confirmed present, shipping from `packages/nodes-base/nodes/ExecuteWorkflow/`.
