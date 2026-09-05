---
id: FEAT-sar60r
title: Support bounded loops for batch iteration
status: done
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

- [x] A registered loop node exists with a `done` output and a `loop` output, takes a batch size, and dispatches one batch per iteration.
- [x] A back edge is accepted only when its target is a loop node's input; any other cycle is still rejected with the existing `must not contain a cycle` error.
- [x] The iteration count is bounded by an explicit maximum; exceeding it fails the execution with a message naming the loop node and the bound, rather than running forever or being silently truncated.
- [x] The loop node emits the accumulated items on `done` after the last batch, and the nodes downstream of `done` run exactly once.
- [x] Each iteration of every node in the loop body is recorded as its own row in the execution trace, distinguishable by run index, and the API exposes them in iteration order.
- [x] An execution whose loop body fails on iteration three records the two successful iterations and stops, rather than discarding them.

## Implementation Plan

Take the compiler first. `hasCycle` becomes a check that every back edge — an edge whose target is grey in the DFS — terminates at a node whose definition is marked as a loop entry. Mark it on the definition rather than by node type string: add a boolean to `node.Definition` (and to `workflow.NodeDefinition`, which `cloneNodeDefinition` copies into the IR), so a future Split In Batches variant or a pack-supplied loop node gets the same treatment without the compiler learning a type name. Keep the existing message for every other cycle.

Then the runner. The current single-pass `completed` map cannot express a node running twice, so this ticket lands on the run-index work: outputs are stored per `(nodeID, runIndex)`. Recommended scheduling model — the loop node holds its remaining batches as executor state carried on its own output item rather than in the runner, and the runner resets the completion state of the loop's body subgraph each time the loop node emits on `loop`. Keeping the iteration state in the item rather than in the runner is what stops the runner from acquiring node-specific knowledge, and it is how the state survives into the execution record for free. The alternative — a runner-owned iteration counter keyed by node ID — is simpler to write and harder to inspect afterwards; prefer the item-carried state.

The bound needs two layers. A per-node `maxIterations` parameter with a default, and an execution-wide ceiling from configuration so a malicious or mistaken workflow in a shared install cannot spin. Fail the execution when either is hit; do not silently emit `done`, because a truncated loop that reports success is worse than one that fails.

The trap is persistence. `uidx_node_runs_attempt` collides on the second iteration of any body node, and `internal/engine/service.go:139-166` iterates `result.NodeRuns` with `sequence + 1` as the only ordering key. The run-index column must exist before this ticket can record anything, and `Attempt` must stay reserved for retries — overloading it here makes retry-within-a-loop unrepresentable.

Do not attempt n8n's full Split In Batches parameter surface here. This ticket delivers the engine capability and one native loop node; matching n8n's node exactly, and mapping `n8n-nodes-base.splitInBatches` onto it, is flow-control parity work in a later phase.

## References

- Roadmap plan, p1 section, entry V2-p1-4: `.pine/roadmap.md`.
- `.pine/roadmap.md` — the p4 flow-control paragraph, which owns node parity.
- `internal/workflow/compiler.go` — `hasCycle`, the cycle rejection in `Compile`, `cloneNodeDefinition`.
- `internal/engine/runner.go` — the scheduling loop, `completed`, `nodeInput`.
- `internal/engine/service.go`, `internal/repository/models.go` — node-run persistence and `uidx_node_runs_attempt`.
- `nodes/core.go` — `RegisterAll`, where the loop node registers.

## Outcome

### The compiler

`LoopEntry` is a boolean on the definition rather than a node type the compiler
knows by name, so a pack-supplied loop node gets the same treatment without the
compiler learning a string.

The cycle check was rewritten rather than adjusted, because the obvious version
was **order-dependent and wrong**. Deciding edge by edge during one DFS — "the
edge that closes onto a grey node must target a loop entry" — gives a different
answer depending on where the traversal starts: entering the graph at the loop's
body makes the *entry→body* edge look like the back edge, and that node is not a
loop entry, so the graph is rejected. Go's map iteration order decided whether a
valid workflow compiled. It now identifies the loop's closing edges first, by
asking which edges target a loop entry that can reach their source, removes
them, and checks the remainder for any cycle at all. That is deterministic, and
a five-run repeat confirmed it.

An arbitrary back edge between two ordinary nodes is still rejected, tested
directly against the accepting case.

### The node

`kilasflow.loop` with `done` and `loop` outputs — `done` first, so it is output
index 0 and a workflow that only wires the finished branch behaves like an
ordinary node.

Iteration state rides on the dispatched item under `$loop`, as recommended,
rather than living in the runner. That keeps the runner free of node-specific
knowledge — it schedules a graph, it does not know what a loop is — and the
state lands in the execution record for free, so an operator can see which batch
a run was on when it failed. The bookkeeping is stripped before anything
downstream sees it, which is asserted.

### The runner

Three rules, and the ordering between them is the whole difficulty:

1. A back edge is hidden from scheduling until the loop has started, or the
   entry waits forever on a body that cannot run until the entry does.
2. Exactly one side feeds any dispatch — upstream on the first, the body on
   every one after. Taking both would restart the loop each iteration; taking
   the back edge first reads an output that does not exist.
3. The body is reopened when a batch is dispatched, and the entry when the body
   returns. Doing both at once leaves neither able to run, which is the deadlock
   the first attempt produced.

### One thing the ticket did not anticipate

The `done` branch had to be **held back** while the loop iterates. Branch pruning
from FEAT-k3grr5 made this a real defect rather than an inefficiency: a node
below `done` scheduled mid-loop receives an empty stream, is pruned as an
untaken branch, and is therefore *complete* — so it never runs when the final
batch actually carries the accumulated items. `awaitingLoop` keeps it
unschedulable until the loop emits on `done`.

### Bounds

Two layers as specified: a per-node `maxIterations` and an instance-wide
`workflow.MaxLoopIterations` ceiling, both validated at compile time. Exceeding
either fails the execution naming the node and the bound — never a silent `done`,
because a truncated loop reporting success hands downstream nodes a partial
result they cannot distinguish from a complete one. The iterations that did
succeed stay in the trace rather than being discarded.

### Scope held

n8n's full Split In Batches parameter surface is not attempted, and
`n8n-nodes-base.splitInBatches` is not mapped. This delivers the engine
capability and one native loop node; matching n8n's node is flow-control parity
work in p4.
