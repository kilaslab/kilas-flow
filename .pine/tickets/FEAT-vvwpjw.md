---
id: FEAT-vvwpjw
title: Reach parity on the flow-control node family
status: todo
priority: medium
labels:
    - nodes
    - parity
deps:
    - FEAT-k3grr5
    - FEAT-fw0m2q
    - FEAT-sar60r
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T05:00:31Z"
updated: "2026-09-05T05:00:31Z"
---

## Scope

KilasFlow ships two flow-control nodes. `RegisterAll` in `nodes/core.go` registers `kilasflow.if` and `kilasflow.merge`, and nothing else in this family exists: there is no Switch, no Filter, no Split In Batches, no Limit and no No Op. IF accepts exactly one condition — `ifCondition` in `nodes/executors.go` returns "IF conditions must contain exactly one condition" for any array whose length is not 1 — over four operators (`equals`, `notEquals`, `exists`, `notExists`) compared with `reflect.DeepEqual`, so `"5"` never equals `5` and there is no combinator, no type coercion and no loose validation. Merge declares two fixed inputs named `input1` and `input2` and a `mode` select whose only option is `append`; `executeMerge` fails on anything else.

The importer makes this visible rather than survivable. `mappings` in `internal/interop/n8n/n8n.go` carries ten entries and none of them is `switch`, `filter`, `splitInBatches`, `limit` or `noOp`, so every one of those becomes `kilasflow.unsupported`, whose validator always fails (`nodes/unsupported.go`). A single Switch therefore makes an entire imported workflow unactivatable. Merge is mapped but not honoured: `mergeToKilas` in `internal/interop/n8n/parameters.go` rewrites every n8n mode to `append` and reports an issue whose own words are "changes what this node does" — accurate, and useless to someone who imported a `combine`-mode Merge.

This ticket closes the family: Switch, Filter, the real Merge modes, Split In Batches, Limit and No Op, each as a native Go node with an importer mapping in both directions. Success is measured against the p0 corpus in `imported / activatable / executable` counts, not in a node count.

Two engine facts constrain the work and belong to other tickets. `Runner.Run` in `internal/engine/runner.go` aborts the execution when a node returns a different number of output streams than `node.Definition.Outputs` declares, and `Definition.Outputs` is a static `[]workflow.Port` — a Switch whose output count comes from its own rules cannot be expressed until p2-8 settles whether ports may be computed from parameters. And `hasCycle` in `internal/workflow/compiler.go` rejects every cycle, so the Split In Batches feedback edge is unrepresentable until p1-4 lands bounded loops.

## Acceptance criteria

- [ ] Switch is a registered node whose outputs come from its own rules, routes each item to the first matching rule (or to all matching rules when the n8n `allMatchingOutputs` option is set), supports the fallback output, and renames outputs from `renameOutput`/`outputKey`.
- [ ] Filter is a registered node that emits only the items whose conditions matched, using the same condition evaluator as IF and Switch.
- [ ] IF, Filter and Switch share one condition engine that supports multiple conditions, `and`/`or` combinators, n8n's operator set per value type (string, number, boolean, dateTime, array, object), and n8n's loose type validation — with the coercion rules covered by table tests rather than inferred.
- [ ] Merge supports append, combine by matching fields, combine by position, combine all (cross join) and choose branch, and honours n8n's configurable input count instead of two fixed inputs.
- [ ] Split In Batches (n8n's "Loop Over Items") is a registered node with `done` and `loop` outputs, a batch size, `reset`, and a bounded iteration count enforced by the engine, and it produces per-iteration execution evidence.
- [ ] Limit and No Op are registered, with Limit honouring `maxItems` and `keep` (first items / last items).
- [ ] `internal/interop/n8n` maps `n8n-nodes-base.switch`, `.filter`, `.merge`, `.splitInBatches`, `.limit` and `.noOp` in both directions, `SupportedMappings()` lists them, and no option is dropped without a named import diagnostic.
- [ ] A workflow in the p0 corpus containing a Switch or a Filter imports, activates and runs, and a not-taken branch produces no downstream node run.

## Implementation Plan

Do the cheap nodes first to prove the registration and import path end to end: No Op and Limit are a definition in a new `nodes/flow.go`, an executor entry in `RegisterExecutors` (`nodes/executors.go`), a mapping in `internal/interop/n8n/n8n.go` and a fixture. Once that round trip is green, the rest is real work.

Extract the condition engine before writing Filter or Switch. `ifCondition` and `condition.matches` in `nodes/executors.go` are the whole of KilasFlow's condition support today and they are private to the `nodes` package, single-condition and equality-only. Move them into a package of their own (`internal/conditions`) with n8n's shape — an ordered list of `{leftValue, operator: {type, operation}, rightValue}` plus a combinator and an options bag — and reimplement IF on top of it in the same commit, so there is one evaluator rather than three. This is the largest and least visible part of the ticket; budget for it. n8n's own semantics live in `packages/nodes-base/nodes/If/V2/` and its test fixtures in `packages/nodes-base/nodes/If/test/v2/` in the reference checkout, which are exactly the cases to port as table tests.

Merge next, because it needs no engine change: widen `mergeNode()` in `nodes/core.go` to declare inputs from a `numberInputs` parameter and rewrite `executeMerge`. Then Switch, which does need an engine change. There are two ways to give it a variable output count. Either declare a fixed maximum number of ports and leave the unused ones empty, which keeps `Definition.Outputs` static and costs nothing in the engine but shows dead ports on the canvas and breaks the import of a Switch with more rules than the maximum; or let a definition compute its ports from the node's own parameters, which is the question p2-8 already has to answer for the AI Agent's conditional slots. Take the second: implement Switch against p2-8's computed ports and do not add a fixed-maximum stopgap that would have to be unwound.

Split In Batches goes last because it cannot work until p1-4 allows a bounded cycle through a designated loop node. Its `loop` output feeds the loop body and the body's tail feeds back into its input; the node holds the remaining items between iterations, which means it needs per-execution node state the runner does not have today — add it as an explicit field on the node's run record rather than a package-level map, or two concurrent executions of the same workflow will share a cursor.

The trap that will bite whoever picks this up is in `Runner.Run`: `if got, want := len(output), len(node.Definition.Outputs); got != want` aborts the whole execution. A Switch executor that returns only the matched branch's stream, or a Filter that returns nothing when everything was filtered out, fails there rather than at the node. Every executor in this family must return exactly one stream per declared port, empty where nothing matched — and p1-1 is what makes an empty stream mean "do not run downstream" instead of "run downstream with a synthetic item".

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section p4 (flow control) and the p1 tickets it depends on — p1-1 branch pruning and p1-4 bounded loops.
- PRD `gflow-prd-v1.md` §21 Connection Types, §23 Node Registry, §24 Native V1 Nodes.
- Verified in this repository: `nodes/core.go` (`ifNode`, `mergeNode`), `nodes/executors.go` (`executeIF`, `executeMerge`, `ifCondition`, `condition.matches`), `internal/engine/runner.go` (`Runner.Run` output-arity check), `internal/workflow/compiler.go` (`hasCycle`), `internal/interop/n8n/n8n.go` (`mappings`), `internal/interop/n8n/parameters.go` (`mergeToKilas`), `nodes/unsupported.go`.
- n8n 2.34.0 reference checkout (read-only, outside this repo): `/Users/izzadev/projects/mitrachat/n8n/packages/nodes-base/nodes/If/V2/`, `.../nodes/If/test/v2/`. The type strings `n8n-nodes-base.switch`, `.merge` and `.noOp` are confirmed present in that checkout; confirm `.filter`, `.splitInBatches` and `.limit` against the widened checkout from p0-1 before writing the mapping table.
