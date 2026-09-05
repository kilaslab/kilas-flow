---
id: FEAT-k3grr5
title: Prune untaken branches so a skipped path never executes
status: done
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T04:59:32Z"
updated: "2026-09-05T04:59:32Z"
---

## Scope

`internal/engine/runner.go` schedules a node the moment every upstream node has *completed*, not when an incoming edge actually carried items. `dependenciesComplete` (runner.go:309) asks only whether `completed[edge.Source.NodeID]` exists. `nodeInput` (runner.go:318) then builds an input map in which the untaken port contributes zero items, and every executor reads an empty `main` port as "run once against an empty `$json`": `nodes/http.go:174-176`, `nodes/ai.go:341-343`, `nodes/database.go:199-201`, and `executeRespond` in `nodes/webhook.go:225-227` all substitute `[]workflow.Item{{JSON: map[string]any{}}}`. The combined effect is that an IF whose false branch matched no items still fires one HTTP request, one model call, one SQL statement and one webhook response on the branch that was not taken. `executeIF` (nodes/executors.go:83-95) is correct — it returns an empty stream for the branch nobody matched — so the defect is entirely in scheduling and in the executors' fallback.

This is the single most damaging engine bug for n8n compatibility. Branching is the second most common construct in the import corpus, and today every imported IF sends real traffic down both arms. It is also a security and cost problem, not only a correctness one: the untaken arm can be an HTTP call to a third party or a paid model completion.

A second defect sits on the same path. `findResponse` (internal/webhook/webhook.go:331) iterates `map[string][][]workflow.Item` — a Go map — and returns the first item carrying `$response`. With one Respond to Webhook node on each IF branch, both run today, both write `$response`, and which one answers the HTTP caller is decided by Go's randomized map iteration order. Pruning removes the ambiguity for the common case; the lookup must still become deterministic so that a graph which legitimately produces two responses answers with a defined one rather than a coin flip.

Skipping must also be visible. `internal/engine/service.go:139-166` persists one `execution.NodeRun` per entry in `Result.NodeRuns`, and `execution.Status` (internal/execution/records.go:13-18) has no value between succeeded and failed. A skipped node that simply vanishes from the trace would make an execution look as though the branch never existed.

## Acceptance criteria

- [x] A node that declares at least one `main` input port runs only when at least one incoming `main` edge delivered one or more items; otherwise its executor is never invoked.
- [x] Skipping cascades: a skipped node yields empty streams on every declared output port, so the same rule skips everything downstream of it.
- [x] Typed attachment edges (`ai_languageModel`, `ai_memory`, `ai_tool`) never gate scheduling, and a node with no declared inputs still runs unconditionally.
- [x] The empty-item substitution in `nodes/http.go`, `nodes/ai.go`, `nodes/database.go` and `executeRespond` survives only for a node that genuinely has no incoming `main` edge, and is unreachable for a node whose incoming edges all delivered nothing.
- [x] A skipped node is recorded in the execution trace as a distinct state, not as succeeded, not as failed, and not as an absent row; the API surface exposes it and the OpenAPI contract is regenerated.
- [x] A fixture workflow with an HTTP node, an AI node, a SQL node and a Respond node on the untaken arm of an IF records zero outbound calls on that arm.
- [x] With a Respond to Webhook node on each IF branch, the HTTP caller receives the response from the branch that ran, and repeated executions of the same input return the same response.

## Implementation Plan

Change `runner.go` first and alone. Replace `dependenciesComplete` with a two-part predicate: a node is *schedulable* when every upstream node has reached a terminal state (completed or skipped), and it is *live* when it has no `main` input ports or at least one incoming `main` edge whose source produced items on the referenced `SourceOutputIndex`. Skipping is expressed by writing an all-empty `workflow.NodeOutput` sized to `len(node.Definition.Outputs)` into `completed[nodeID]` without calling the executor, which makes the cascade fall out of the existing rule rather than needing a second traversal. Do not skip the `NodeRuns` append — record the run with a skipped marker, or the trace loses the node entirely.

The trap is `firstItem`/`request.NodeOutputs` at runner.go:244-246: a skipped node produces no first item, so `$node["Name"]` must keep returning "not set" rather than an empty object that reads as a successful lookup. Do not register a skipped node's name in `NodeOutputs`.

Then carry the state outward. `engine.NodeRun` needs a field the service can map onto a new `execution.Status` value; add `StatusSkipped` in `internal/execution/records.go` and map it in `internal/engine/service.go:145-163`, where `status` is currently derived from `run.Error != nil` alone. `internal/api/handlers/workflows.go:488-494` serializes node runs, so this is an OpenAPI change: run `pnpm generate:api` in `web/` and `pnpm generate:types` in `sdk/`, or the drift checks fail. The editor's execution canvas (`web/src/lib/components/workflow-editor/execution-canvas-node.svelte`) should render skipped visibly differently from succeeded, otherwise a pruned branch looks like a branch that ran and produced nothing.

Finally, make the response lookup deterministic. `findResponse` should walk the compiled graph's node order rather than a map: pass the ordered `NodeRuns` (or the IR's topological order) instead of `record.Output`, and take the response from the last Respond node that actually ran. Two design options exist for a graph that still yields more than one response after pruning — answer with the first in topological order, or fail the request with an explicit "this workflow produced two responses" error. Recommendation: answer with the first in topological order and record a diagnostic, because a webhook caller waiting on a response is better served by a defined answer than by a 500.

Only the empty-item fallbacks belong to the executors; do not add per-executor "was I pruned?" checks. The whole point is that the runner never calls a pruned node.

## References

- Roadmap plan, p1 section, entry V2-p1-1: `.pine/roadmap.md`.
- `internal/engine/runner.go` — `dependenciesComplete`, `nodeInput`, `firstItem`, the scheduling loop in `Run`.
- `nodes/http.go`, `nodes/ai.go`, `nodes/database.go`, `nodes/webhook.go` — the four empty-item substitutions.
- `internal/webhook/webhook.go` — `findResponse`, `respondFromExecution`.
- `internal/engine/service.go`, `internal/execution/records.go`, `internal/api/handlers/workflows.go` — node-run persistence and the API surface a new status changes.

## Outcome

### The scheduling change

`dependenciesComplete` kept its job — deciding *when* a node may be considered —
and a new `isLive` decides *whether* it runs. A node runs when it declares no
`main` input, or when some incoming item edge delivered at least one item on the
port it was wired to.

Skipping is an all-empty `NodeOutput` written straight into `completed` without
calling the executor, so the cascade downstream falls out of the same rule
rather than needing a second traversal.

Typed attachment edges never gate scheduling, asserted by
`TestATriggerAndAnAgentAreNeverPruned` — gating on them would skip every agent
in the product, since an agent with no memory is a valid agent.

The trap the ticket named was honoured: a skipped node is **not** registered in
`request.NodeOutputs`, so `$node["Name"]` keeps reporting "not set" rather than
an empty object that reads as a successful lookup.

The empty-item substitution in the executors survives only for a node with no
incoming `main` edge at all — `isLive` returns true when `itemEdges == 0`, and
false when edges exist but delivered nothing.

### Proving it

`TestUntakenBranchNeverInvokesItsExecutors` puts an HTTP node and a Respond node
on the untaken arm and configures the HTTP policy to allow **no** host. The proof
is that `Run` returns no error: if either node had been reached, the execution
would have failed. That is stronger than counting calls through a stub, because
it exercises the real executors.

A first attempt did try to mix counting stubs with real executors and could not
— `RegisterExecutors` refuses a duplicate ID, so the stubs would never have
taken effect and the test would have passed while proving nothing.

### Visibility

`execution.StatusSkipped` is a new node-run status, mapped in
`internal/engine/service.go` from `NodeRun.Skipped`. A skipped node is recorded,
not omitted: an absent row would make a branch look as though it never existed.

One thing the ticket did not mention: the event type. A non-succeeded status
published `node.failed`, so a pruned branch would have lit up red on the live
canvas. Skipped now publishes `node.completed`, and the node-run record carries
the status that tells the two apart.

No OpenAPI change was needed — `status` is published as a plain string rather
than an enum, so both generators produce no diff. The editor already modelled
`skipped` as its client-side fallback for a node with no run record, and the
server value lands on exactly that rendering; only the comment claiming
"`skipped` is not a server status" needed correcting.

### The response lookup

`findResponse` now walks `record.NodeRuns` sorted by `Sequence` — execution
order — instead of iterating a Go map. Pruning removes the ambiguity for the
common case, but a graph can still legitimately produce two responses, and the
first in execution order wins as recommended: a webhook caller waiting on a
reply is better served by a defined answer than by a 500.

`TestBranchedWebhookAnswersFromTheBranchThatRan` sends the same input six times
per branch and asserts the same response every time. Under map iteration that
was a coin flip on each request.

### Corpus effect

None. The corpus scores tier three as "ran to completion", and pruning changes
*which* nodes run rather than whether a workflow compiles — the fixtures that
would show it are still blocked on node types from p3 and p4.
