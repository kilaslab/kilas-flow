---
id: FEAT-9knk67
title: Track paired-item lineage and run index through the runner
status: done
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:00:06Z"
updated: "2026-09-05T05:00:06Z"
---

## Scope

An n8n expression reaches sideways across the graph with `$('Node').item`, which resolves to the item of `Node` that this item descends from. Roughly a third of the expressions in the import corpus use it. KilasFlow has no lineage at all: `internal/engine/runner.go` records only `request.NodeOutputs[node.Name] = first` (runner.go:244-246), and `firstItem` (runner.go:257-264) returns the first item of the first non-empty output port. So the closest thing KilasFlow can offer today is "the first item of a node", which is correct only when every node in the run processed exactly one item — and silently wrong otherwise, which is the worst possible failure mode for an imported workflow.

`workflow.Item` (internal/workflow/document.go:79-82) carries `JSON` and `Binary` and nothing else. There is no field naming the input item an output item came from, and no executor sets one. Every executor that maps items one-to-one — `executeSet`, `executeIF`, `nodes/http.go`, `nodes/database.go`, `nodes/ai.go`, `executeRespond` — loses the correspondence the moment it appends to its result slice. `executeMerge` (nodes/executors.go:98-107) concatenates two ports and destroys it twice over.

The second half is run index. n8n identifies a node's output by `(node, runIndex)` because a node inside a loop or a fan-out produces several distinct runs, and `$('Node').item` has to name which one. KilasFlow persists an `Attempt` column on `execution_node_runs` (internal/repository/models.go:133-149, unique on `(execution_id, node_id, attempt)`), but `internal/engine/service.go:162` hardcodes `Attempt: 1` for every node run, so a node can be recorded exactly once per execution. Bounded loops and per-item fan-out both need more than that, so run index has to land here rather than being retrofitted later.

## Acceptance criteria

- [x] `workflow.Item` carries provenance identifying the source node, its run index, and the index of the input item this item descends from, and it round-trips through JSON persistence unchanged.
- [x] Every built-in executor that maps items one-to-one propagates provenance; `executeMerge` preserves the provenance of each side rather than flattening it.
- [x] The runner records each node's output per run index, so a node that ran twice has two retrievable output sets rather than one that overwrote the other.
- [x] `execution_node_runs` records a run index distinct from `Attempt`, and a node that ran more than once in an execution produces more than one row without violating a unique index.
- [x] Given `A → Set → B` over three items, an expression evaluated on item 2 of `B` that reaches back to `A` resolves to item 2 of `A`, not item 1.
- [x] When lineage genuinely cannot be established — after a Merge that combined unrelated streams, or after a node that produced a different item count — the lookup fails with a message naming the node and the reason, rather than returning the first item.

## Implementation Plan

Design the provenance shape before touching an executor. n8n's `pairedItem` is `{item, input?}` on the output item, pointing at the *incoming* item index. KilasFlow needs slightly more because its ports are named rather than positional: recommend `PairedItem{SourceNodeID string, SourcePort string, RunIndex int, ItemIndex int}` as an optional field on `workflow.Item`, JSON tag `pairedItem,omitempty` so existing persisted items decode unchanged. Do not model it as a list of candidates the way n8n allows for aggregating nodes; a single origin plus an explicit "lineage lost" marker is easier to reason about and is enough for every expression in the corpus.

Change the runner next. `completed` is a `map[string]workflow.NodeOutput`; it becomes a map to a slice of outputs indexed by run index, and `request.NodeOutputs` grows the same dimension. `cloneItem`/`cloneItems` (runner.go:364-381) must copy the new field — they are the choke point through which every item passes, so missing it there is the failure that will not show up until a specific graph shape hits it. The runner stamps provenance where the executor did not: after an executor returns, any output item with an empty `PairedItem` and exactly one incoming `main` port with a matching item count gets one inferred by position. Inference by count is a convenience, not the contract; an executor that reorders or filters must set provenance itself, and `executeIF` and `executeMerge` are the two that must.

Persistence is the trap. `uidx_node_runs_attempt` is unique on `(execution_id, node_id, attempt)`, so the second run of a looped node collides today. Add a `run_index` column and widen that index to `(execution_id, node_id, attempt, run_index)` rather than overloading `Attempt` — attempt means "this is retry N of the same run" and run index means "this is the Nth time the node ran", and conflating them makes the retry work in the error-handling ticket unimplementable. This is a schema change under `AutoMigrate` today; keep it additive so an existing SQLite database still opens.

Do not wire `$('Node')` syntax here. This ticket delivers the lineage; the expression grammar that reads it is a separate ticket, and landing the two together makes both untestable in isolation. Prove the lineage instead with a Go-level test that inspects `Result.NodeRuns` provenance directly.

## References

- Roadmap plan, p1 section, entry V2-p1-2: `.pine/roadmap.md`.
- `internal/engine/runner.go` — `firstItem`, `NodeOutputs`, `cloneItem`, the `completed` map.
- `internal/workflow/document.go` — `Item`, `NodeInput`, `NodeOutput`.
- `nodes/executors.go` — `executeSet`, `executeIF`, `executeMerge`, `cloneItem`.
- `internal/repository/models.go` — `executionNodeRunModel`, `uidx_node_runs_attempt`.
- `internal/engine/service.go` — the node-run persistence loop that hardcodes `Attempt: 1`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 14 — the inline expression Result preview steps per item (`Item ‹ ›`) — the UI affordance that only makes sense once paired-item lineage exists. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Outcome

### Provenance

`workflow.PairedItem{SourceNodeID, SourcePort, RunIndex, ItemIndex, Lost}` as an
optional `pairedItem` field, so every already-persisted item decodes unchanged.

A single origin rather than n8n's list of candidates, as recommended, plus an
explicit `Lost` marker. A list would make every consumer handle an ambiguity
only aggregating nodes can produce; one origin and an honest "cannot say" covers
every expression in the corpus and is what lets a lookup fail with a reason
instead of returning the first item — which is correct only when every node
processed exactly one item and silently wrong otherwise.

### Where it is stamped

Both `cloneItem` implementations carry it — the runner's and the node pack's.
They are the choke point every item passes through, and the ticket is right that
missing it there is the failure that stays invisible until one graph shape hits
it.

The runner infers by position only when the inference is sound: exactly one
incoming item port and a matching item count. A one-to-one node then **inherits**
the origin its input already carried rather than pointing at its immediate
predecessor, which is what makes `A → Set → B` resolve back to `A`.

Two executors set their own, because the runner's inference would be wrong for
them:

- **IF** filters, so positions shift. Each routed item keeps the origin it
  arrived with; renumbering by position is exactly the wrong answer, and the
  test asserts the true branch's second item still names input item 2.
- **Merge** concatenates unrelated streams, so each side keeps its own
  provenance rather than being flattened.

A node that changes its item count is marked `Lost` rather than guessed.

### Run index

`NodeRun.RunIndex` and a `run_index` column that **widens** `uidx_node_runs_attempt`
rather than overloading `Attempt`. The distinction is load-bearing: attempt means
"retry N of the same run", run index means "the Nth run", and conflating them
would make the retry work from FEAT-a6yg3n unimplementable inside a loop.
`TestRunIndexIsRecordedPerRun` pins it by retrying a node and asserting two
attempts at run index 0.

Outputs are kept per run index in the runner, so a node that ran twice has two
retrievable output sets. The column is additive and defaulted, so an existing
SQLite database still opens.

`runIndex` is exposed on the node-run API resource and both clients were
regenerated.

### Two things that needed fixing along the way

1. A service test pinned the persisted node-run JSON byte for byte, so adding a
   field to an item failed it. It now asserts content — including that
   provenance survives the round trip through durable storage, which is the
   acceptance criterion — rather than a serialization that any future item field
   would break again.

2. Two frontend test fixtures had to gain `runIndex`, because the generated type
   makes it required.

### Not done here, deliberately

`$('Node')` syntax is not wired. The lineage is proven by Go tests that inspect
`Result.NodeRuns` provenance directly; the expression grammar that reads it is
FEAT-v8k1tc, and landing both together would make each untestable in isolation.
