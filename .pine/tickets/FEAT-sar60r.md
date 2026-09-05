---
id: FEAT-sar60r
title: Support bounded loops for batch iteration
status: todo
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:01:17Z"
updated: "2026-09-05T05:01:17Z"
---

## Scope

`hasCycle` (internal/workflow/compiler.go:392-424) is a plain three-colour DFS over every edge, and `Compile` rejects the document outright when it returns true (compiler.go:255-262, `workflow graph must not contain a cycle`). n8n's Split In Batches / Loop Over Items pattern is a cycle by construction: the loop node's `loop` output runs the body, and the body's last node wires back into the loop node's input so the next batch is dispatched. Every workflow built on that pattern is unrepresentable in KilasFlow today, and the pattern is common enough in the import corpus that refusing it caps how many templates can ever activate.

The runner is equally unprepared. `Runner.Run` (internal/engine/runner.go:159-253) loops `for len(completed) < len(ir.Nodes)`, writes each node's output into `completed[nodeID]` exactly once, and would deadlock on `compiled workflow graph has no schedulable node` the moment a genuine cycle existed. `execution_node_runs` is unique on `(execution_id, node_id, attempt)` (internal/repository/models.go:133-149) and `internal/engine/service.go:162` writes `Attempt: 1` for every row, so a node that ran five times would collide on the second insert.

A general cycle is not wanted and is not what this ticket delivers. What is wanted is a *designated* loop node with an explicit iteration bound, so that a back edge is legal only when it closes onto such a node, the number of iterations is finite and knowable before the run starts, and every iteration leaves its own evidence in the execution trace. Anything else — an arbitrary back edge between two ordinary nodes — stays rejected exactly as it is today.

## Acceptance criteria

- [ ] A registered loop node exists with a `done` output and a `loop` output, takes a batch size, and dispatches one batch per iteration.
- [ ] A back edge is accepted only when its target is a loop node's input; any other cycle is still rejected with the existing `must not contain a cycle` error.
- [ ] The iteration count is bounded by an explicit maximum; exceeding it fails the execution with a message naming the loop node and the bound, rather than running forever or being silently truncated.
- [ ] The loop node emits the accumulated items on `done` after the last batch, and the nodes downstream of `done` run exactly once.
- [ ] Each iteration of every node in the loop body is recorded as its own row in the execution trace, distinguishable by run index, and the API exposes them in iteration order.
- [ ] An execution whose loop body fails on iteration three records the two successful iterations and stops, rather than discarding them.

## Implementation Plan

Take the compiler first. `hasCycle` becomes a check that every back edge — an edge whose target is grey in the DFS — terminates at a node whose definition is marked as a loop entry. Mark it on the definition rather than by node type string: add a boolean to `node.Definition` (and to `workflow.NodeDefinition`, which `cloneNodeDefinition` copies into the IR), so a future Split In Batches variant or a pack-supplied loop node gets the same treatment without the compiler learning a type name. Keep the existing message for every other cycle.

Then the runner. The current single-pass `completed` map cannot express a node running twice, so this ticket lands on the run-index work: outputs are stored per `(nodeID, runIndex)`. Recommended scheduling model — the loop node holds its remaining batches as executor state carried on its own output item rather than in the runner, and the runner resets the completion state of the loop's body subgraph each time the loop node emits on `loop`. Keeping the iteration state in the item rather than in the runner is what stops the runner from acquiring node-specific knowledge, and it is how the state survives into the execution record for free. The alternative — a runner-owned iteration counter keyed by node ID — is simpler to write and harder to inspect afterwards; prefer the item-carried state.

The bound needs two layers. A per-node `maxIterations` parameter with a default, and an execution-wide ceiling from configuration so a malicious or mistaken workflow in a shared install cannot spin. Fail the execution when either is hit; do not silently emit `done`, because a truncated loop that reports success is worse than one that fails.

The trap is persistence. `uidx_node_runs_attempt` collides on the second iteration of any body node, and `internal/engine/service.go:139-166` iterates `result.NodeRuns` with `sequence + 1` as the only ordering key. The run-index column must exist before this ticket can record anything, and `Attempt` must stay reserved for retries — overloading it here makes retry-within-a-loop unrepresentable.

Do not attempt n8n's full Split In Batches parameter surface here. This ticket delivers the engine capability and one native loop node; matching n8n's node exactly, and mapping `n8n-nodes-base.splitInBatches` onto it, is flow-control parity work in a later phase.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, entry V2-p1-4, and the p4 flow-control paragraph that owns node parity.
- `internal/workflow/compiler.go` — `hasCycle`, the cycle rejection in `Compile`, `cloneNodeDefinition`.
- `internal/engine/runner.go` — the scheduling loop, `completed`, `nodeInput`.
- `internal/engine/service.go`, `internal/repository/models.go` — node-run persistence and `uidx_node_runs_attempt`.
- `nodes/core.go` — `RegisterAll`, where the loop node registers.
