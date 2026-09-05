---
id: FEAT-ybm2pd
title: Adopt the cluster-node model for AI sub-nodes
status: todo
priority: high
labels:
    - ai
    - parity
deps:
    - FEAT-55v09k
    - FEAT-afs850
parent: EPIC-m42s3g
phase: p5
created: "2026-09-05T05:00:07Z"
updated: "2026-09-05T05:00:07Z"
---

## Scope

KilasFlow already has a sub-node shape, but by accident rather than by design. `nodes/ai.go` gives the chat model, the memory and the HTTP tool a single typed output port each, and each emits one descriptor item under the key `$ai` (`descriptorKey`). The agent reads them back from `input["model"]`, `input["memory"]` and `input["tools"]`. That works, and topological order already guarantees a sub-node runs before the root that reads it — but nothing names the pattern and nothing enforces the cardinality n8n's editor and runtime both guarantee.

The cardinality gap is real and silent. `validateExecutableTopology` in `internal/workflow/compiler.go` builds an `incoming[nodeID][portName]` count and uses it only to check that a `main` port received *something*; every non-`main` port is skipped outright as optional, and there is no upper bound anywhere. On the client, `canConnect` in `web/src/lib/workflow-editor/ports.ts` requires only that the two port kinds match and that the exact edge does not already exist. So two chat models can be wired to one agent, the graph compiles, the workflow activates, and at run time `descriptorFrom` returns whichever descriptor it meets first — the model that actually runs is decided by item order.

This ticket adopts n8n's root/sub-node model explicitly, on top of the port descriptors and the widened `ConnectionKind` set that p2-8 introduces, and applies n8n's slot rules: `ai_languageModel`, `ai_memory` and `ai_outputParser` cap at one connection each, `ai_tool` is uncapped. n8n expresses exactly this through `INodeInputConfiguration` (`category`, `displayName`, `required`, `type`, `filter`, `maxConnections`) in `packages/workflow/src/interfaces.ts` of the reference checkout.

There is a direction trap that will bite anyone reasoning from the picture rather than the JSON. In an n8n workflow document the *sub-node* is the source key of the connection and the root agent is the target — the model connects to the agent, not the other way round. KilasFlow's `workflow.Connection` is also source-to-target, so the mapping is direct; the danger is that "the agent has a model" reads like the agent should be the source, and an importer written on that intuition produces a graph that compiles and never delivers a descriptor.

## Acceptance criteria

- [ ] Every AI node registered by `nodes/ai.go` declares its slots through p2-8's port descriptors, with explicit cardinality: one connection each for `ai_languageModel`, `ai_memory` and `ai_outputParser`, uncapped for `ai_tool`.
- [ ] The compiler refuses a graph that exceeds a slot's cap, naming the node, the port and the count, instead of compiling and choosing one at run time.
- [ ] A slot declared required and left empty fails compilation with a message naming the slot; an optional empty slot still compiles, so an agent with no memory and no tools stays valid.
- [ ] The editor refuses the second connection into a capped slot while it is being dragged, so the conflict is visible before the draft is saved.
- [ ] Descriptor delivery is deterministic: with N tools attached, the agent receives exactly N tool descriptors in a stable, tested order.
- [ ] An n8n cluster imported with the sub-node as the connection source produces the same graph as one built natively, and an export puts the sub-node back on the source side.
- [ ] A test proves that two chat models on one agent is a compile error, not a run-time coin flip.

## Implementation Plan

Start in `internal/workflow/compiler.go`. `validateExecutableTopology` already has the per-port incoming counts it needs, so the cap check is a few lines next to the existing "requires an incoming connection" check. While there, replace the blanket `if port.Kind != ConnectionMain { continue }` with the port descriptor's own `required` flag — that line is what currently makes every typed slot optional, and once ports can say so themselves the special case for `main` disappears.

Then `nodes/ai.go`: the four AI definitions declare their slots. Then the editor: `canConnect` in `web/src/lib/workflow-editor/ports.ts` gains a count of existing edges into the target port and compares it to the descriptor's cap.

The trap is the port wire format. `workflow.Port` in `internal/workflow/compiler.go` has no JSON tags, so it serializes with capital `Name` and `Kind`, and the SPA reads those exact keys — `web/src/lib/workflow-editor/ports.test.ts` and `node-visual.test.ts` are full of `{ Name: 'model', Kind: 'ai_languageModel' }`. Adding tags is a breaking client change and p2-8 owns that decision; do not make it here. Follow whatever casing p2-8 settled on, and regenerate the client with `pnpm generate:api` — `internal/node.Definition` is serialized directly as the `/api/v1/node-types` payload (`internal/api/handlers/nodes.go`), so any change to a port's shape is an OpenAPI change and CI drift checks fail otherwise.

With cardinality enforced upstream, `descriptorFrom` in `nodes/ai.go` can be tightened from "return the first match" to "assert exactly one", which turns the silent wrong-model bug into a loud one on any path that slipped past the compiler.

One design decision remains open. Sub-nodes could stay ordinary scheduled nodes that emit a descriptor item, as today, or become compile-time attachments the runner resolves without executing them. Recommend keeping the descriptor-item model: it works, it keeps topological order as the only scheduling rule, and it gives each sub-node its own row in the execution record. Revisit only when a sub-node must be invoked repeatedly inside a single agent run — a retriever, in p6 — because that is the case the current model genuinely cannot express.

## References

- Roadmap plan, p5 section, entry V2-p5-1: `.pine/roadmap.md`.
- `.pine/roadmap.md` — p2 entry V2-p2-8.
- `/Users/izzadev/projects/mitrachat/n8n/packages/workflow/src/interfaces.ts` — `NodeConnectionTypes` (13 values) and `INodeInputConfiguration`.
- `internal/workflow/document.go` (`ConnectionKind`, `knownConnectionKind`), `internal/workflow/compiler.go` (`Port`, `validateExecutableTopology`), `nodes/ai.go`, `web/src/lib/workflow-editor/ports.ts`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 02 — a real cluster node: one agent with Chat Model*, Memory and Tool slots and four sub-nodes attached. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
