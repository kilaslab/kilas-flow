---
id: FEAT-afs850
title: Import AI connections instead of dropping them
status: done
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

- [x] An n8n workflow whose connections include `ai_languageModel`, `ai_memory` and `ai_tool` imports with those edges present in the canonical document, carrying the matching `ConnectionKind`.
- [x] Each imported AI edge names ports that exist on both endpoints with the same kind, so the document compiles as far as its node types allow.
- [x] A connection kind KilasFlow does not model is still dropped, and its diagnostic names the source node, the target node and the kind — not just the kind.
- [x] Exporting a canonical document with AI edges writes them back under the correct n8n connection key with the sub-node as the source, and a round-trip preserves them.
- [x] The stale comment claiming the AI node family is unsupported is gone, and no diagnostic still says so.
- [x] A fixture covering a LangChain agent with a model, a memory and two tools asserts the exact edge set on import and on round trip.

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

## Outcome

The kind mapping is an identity check against `workflow.KnownConnectionKind`,
now exported for exactly this: n8n's connection-kind strings *are* KilasFlow's
`ConnectionKind` values, so a translation table would only be something to drift
from the definitions. Casing is preserved throughout — nothing in the adapter
normalises a kind or type string.

The directions did agree, as the ticket predicted, so the generic loop needed
the kind and correct ports rather than any rewiring.

### The architectural decision

The catalogue is passed into `Import`, as the plan recommended over a second
hardcoded table. `resolvePort` asks the registry for the first declared port of
the requested kind: n8n identifies an AI endpoint by kind and index, a KilasFlow
node has exactly one port per AI kind, and n8n always writes index 0, so that
resolves unambiguously. The item channel stays positional, because there the
index is meaningful — IF's second output is its false branch.

`Import` gained one parameter and `NewInterop` one dependency, both threaded
from `deps.NodeRegistry` exactly as `NewWorkflows` already does. A nil catalogue
falls back to the positional names, which can only resolve the item channel;
that keeps the function usable without a registry rather than panicking.

### The trap

The ticket flags that an AI edge onto an unsupported placeholder would fail with
`ErrorUnknownPort` — worse than today's silent drop, because the workflow could
not even be saved and inspected. FEAT-t5q318 had already landed the placeholder
port work, so the preferred path was available: every placeholder now declares
one port of each AI kind in **both** directions, alongside its `main` arity.

Both directions matter because a placeholder stands in for either half of an AI
edge — the agent that consumes a model, or the model that supplies one. It costs
nothing: the compiler requires only incoming `main` connections and treats a
typed attachment port as optional by nature.

The fallback path is implemented too, and is what happens when a port genuinely
does not exist: the edge is *held back* with a diagnostic naming which endpoint
lacks the port, rather than dropped silently or recorded onto a port that would
fail compilation.

### Diagnostics

A kind KilasFlow does not model — `ai_vectorStore`, say — is still dropped, and
the message now names the source node, the target node and the channel. Saying
only that a kind was dropped left a user no way to find which two nodes stopped
being joined.

### Export

AI edges are written back under their own channel key with the sub-node as the
source and `Type` set to the kind rather than a hardcoded `"main"`. A typed
channel is not positional, so it always takes slot zero and target index zero;
only the item channel consults `portIndex` and `inputIndexFor`.

`TestImportKeepsAIConnections` asserts the exact five-edge set for an agent with
a model, a memory and two tools, and additionally verifies every edge names
ports that exist on both endpoints with the matching kind — the property that
decides whether the document compiles at all.
