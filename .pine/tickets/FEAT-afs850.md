---
id: FEAT-afs850
title: Import AI connections instead of dropping them
status: todo
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:06:57Z"
updated: "2026-09-05T05:06:57Z"
---

## Scope

`importConnections` refuses every non-`main` connection kind (internal/interop/n8n/n8n.go:327-336) behind a comment that is no longer true: "n8n's ai_* channels exist but their node family is outside the advertised subset, so importing the edge would connect nothing." KilasFlow has had that node family since V1. `workflow.ConnectionKind` declares `ai_languageModel`, `ai_memory` and `ai_tool` alongside `main` (internal/workflow/document.go:20-24), and `nodes/ai.go` registers four nodes that use them: `kilasflow.chatModel` emits on a `model` port of kind `ai_languageModel` (ai.go:49), `kilasflow.memoryBuffer` on `memory` / `ai_memory` (ai.go:73), `kilasflow.httpTool` on `tool` / `ai_tool` (ai.go:95), and `kilasflow.agent` takes all three as inputs alongside `main` (ai.go:120-125).

So an imported LangChain workflow loses its entire wiring. The agent arrives as an unsupported placeholder with no model, no memory and no tools attached, and the diagnostic says only that a connection kind was dropped — not which nodes it would have joined. When the AI parity work lands, it lands against imported graphs that have no AI edges in them at all, so there is nothing to test it against.

The directions agree, which makes this smaller than it looks. n8n keys `connections` by the *source* node's name, and for an `ai_languageModel` channel the source is the sub-node and the target is the agent. KilasFlow is the same shape: the chat model declares the port as an output and the agent declares it as an input. The generic loop in `importConnections` is therefore already directionally correct; what it lacks is a kind mapping and correct port names.

Port naming is where it will break. `outputPortName` and `inputPortName` (n8n.go:368-396) return `"main"` for every canonical type that is not IF or Merge, and the compiler enforces both the port name and the kind: an edge whose endpoints do not exist gives `ErrorUnknownPort`, and one whose kind does not match both ports gives `ErrorIncompatiblePort` (internal/workflow/compiler.go:216-231). An `ai_languageModel` edge wired to `main` fails both.

## Acceptance criteria

- [ ] An n8n workflow whose connections include `ai_languageModel`, `ai_memory` and `ai_tool` imports with those edges present in the canonical document, carrying the matching `ConnectionKind`.
- [ ] Each imported AI edge names ports that exist on both endpoints with the same kind, so the document compiles as far as its node types allow.
- [ ] A connection kind KilasFlow does not model is still dropped, and its diagnostic names the source node, the target node and the kind — not just the kind.
- [ ] Exporting a canonical document with AI edges writes them back under the correct n8n connection key with the sub-node as the source, and a round-trip preserves them.
- [ ] The stale comment claiming the AI node family is unsupported is gone, and no diagnostic still says so.
- [ ] A fixture covering a LangChain agent with a model, a memory and two tools asserts the exact edge set on import and on round trip.

## Implementation Plan

Map the kind first: n8n's connection-kind strings are already exactly KilasFlow's `ConnectionKind` values, so the mapping is an identity check against a closed set rather than a translation table. Note the casing — `ai_languageModel`, camel-cased after the underscore — and never normalise a type or kind string to lower case anywhere in this adapter.

Port naming is the real work. Replace the hardcoded `outputPortsFor` / `inputPortsFor` switches with a lookup that asks the node registry for the definition's declared ports of the requested kind. n8n identifies an AI connection only by kind and index, and a KilasFlow node has exactly one port per AI kind today, so "the first declared port whose kind matches" resolves it unambiguously. Doing it through the registry rather than another hardcoded table is what stops this from having to be rewritten when generated node packs arrive; the cost is that `Import` gains a dependency on the catalogue, which it does not have today. That is the one architectural decision in this ticket — pass a `workflow.Catalog` into `Import`, or keep a second hardcoded table. Recommendation: pass the catalogue. The adapter already needs to know canonical port names to be correct, and inferring them from a duplicated table is how `outputIndexesFor` and `outputPortsFor` came to need a comment explaining that one is the inverse of the other.

The trap is the unsupported placeholder. A LangChain node type has no mapping yet, so it imports as `kilasflow.unsupported`, which declares only a `main` input and a `main` output. An `ai_languageModel` edge onto it will fail compilation with `ErrorUnknownPort` — a *worse* outcome than today's silent drop, because the workflow now cannot even be saved and inspected. This ticket must therefore either land after the placeholder learns to declare ports matching the connections it received, or record the AI edges only when both endpoints declare the port. Prefer the former; if the placeholder work has not landed, take the latter and emit a diagnostic saying the edge was held back rather than dropped.

Export is the mirror: `Export` skips every non-`main` connection with a diagnostic (n8n.go:504-510). Emit the AI edges under their own key in the `Connections` map, keyed by the source node's name, with `Type` set to the kind rather than hardcoded `"main"` (n8n.go:526).

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, entry V2-p1-13, and the p5 AI parity section that consumes the result.
- `internal/interop/n8n/n8n.go` — `importConnections` and its stale comment, `outputPortName`, `inputPortName`, `outputPortsFor`, `inputPortsFor`, the export connection loop.
- `internal/workflow/document.go` — `ConnectionKind` and its four values.
- `nodes/ai.go` — `chatModelNode`, `memoryNode`, `httpToolNode`, `agentNode` and their declared ports.
- `internal/workflow/compiler.go` — the port and kind checks that reject a mismatched edge.
