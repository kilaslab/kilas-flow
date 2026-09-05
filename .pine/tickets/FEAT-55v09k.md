---
id: FEAT-55v09k
title: Describe ports fully and widen the connection kinds
status: todo
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-afs850
    - FEAT-qcm5ec
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T05:04:28Z"
updated: "2026-09-05T05:04:28Z"
---

## Scope

`workflow.Port` in `internal/workflow/compiler.go` is two fields: `Name string` and `Kind ConnectionKind`. A port cannot say what it is called in the UI, whether it must be connected, how many edges it accepts, or which node types may connect to it. The AI Agent's slots — one language model, one memory, many tools — are therefore unenforceable: the compiler will happily accept three language models on one agent, and the editor has no rule to refuse the second.

`ConnectionKind` in `internal/workflow/document.go` has four values: `main`, `ai_languageModel`, `ai_memory`, `ai_tool`. n8n has thirteen. From `NodeConnectionTypes` in the 2.34.0 reference: `main`, `ai_agent`, `ai_chain`, `ai_document`, `ai_embedding`, `ai_languageModel`, `ai_memory`, `ai_outputParser`, `ai_retriever`, `ai_reranker`, `ai_textSplitter`, `ai_tool`, `ai_vectorStore`. The casing is lowerCamel after the `ai_` prefix — `ai_languageModel`, never `ai_language_model` — and these strings appear verbatim in imported workflow JSON, so a normalising or snake-casing transform anywhere in the import path silently drops edges. p5 cannot model output parsers, embeddings or vector stores until the missing nine exist; p6's pgvector work needs `ai_embedding` and `ai_vectorStore` specifically.

The allowlist is duplicated. `knownConnectionKind` exists twice with identical bodies — once in `internal/workflow/document.go`, validating a document's connections, and once in `internal/node/registry.go`, validating a definition's ports. Widening one and not the other produces the worst possible failure: a document that validates and a definition that cannot register, or the reverse, with no error naming the mismatch.

And there is a client-visible trap. `workflow.Port` has **no JSON tags**, so it serializes with capital keys. The generated model in `web/src/lib/api/generated/models/port.ts` is `{Kind: string; Name: string}`, `node-visual.ts` tests `port.Kind !== 'main'`, and `ports.ts` compares `source.Kind !== target.Kind`. Adding tags is a breaking change for every client of `/api/v1/node-types`, including the published SDK. It should still be done here — this is the last point before packs and external SDK consumers exist — but it has to be done as one change across the Go struct, both generated clients and every SPA reader, not incrementally.

## Acceptance criteria

- [ ] `workflow.Port` carries `displayName`, `required`, `maxConnections` and a node-type filter alongside name and kind, and gains lower-camel JSON tags in the same change as the SPA and both generated clients.
- [ ] `ConnectionKind` covers all thirteen n8n values with byte-identical strings, and a test asserts the exact spelling of each so a casing regression fails loudly.
- [ ] The two `knownConnectionKind` implementations are collapsed into one authority that both the document validator and the registry validator call.
- [ ] The compiler enforces `maxConnections` and `required`: a graph exceeding a port's connection limit, or leaving a required port unconnected, fails compilation with the port named.
- [ ] A port's node-type filter is enforced at compile time, not only in the editor, so an imported document cannot bypass it.
- [ ] The editor refuses a connection that would exceed `maxConnections` and labels ports by `displayName` where one is declared, falling back to the port name.
- [ ] `web/pnpm generate:api:check`, `sdk/pnpm generate:types:check`, `pnpm check` and the existing `ports.test.ts` and `node-visual.test.ts` all pass against the retagged model.

## Implementation Plan

Change `internal/workflow/document.go` and `internal/workflow/compiler.go` first: the kind constants, the single `knownConnectionKind`, the widened `Port`, and the JSON tags. Then the compiler's enforcement — `maxConnections`, `required` and the filter — which belongs beside the existing per-edge port checks that already produce `ErrorUnknownPort` and `ErrorIncompatiblePort`; add named error codes rather than reusing those, because "this port is full" and "this port does not exist" are different problems for a user to fix.

Then `internal/node/registry.go`'s `validatePorts`, which today only checks a non-empty name, a known kind and uniqueness. Then the SPA in one pass: `web/src/lib/workflow-editor/ports.ts` (`canConnect`, `lookupPort`, `connectionFromCanvas`), `node-visual.ts` (`isAttachment`, `mainPorts`, `attachmentPorts`), and both generated clients. Do not leave a compatibility shim reading both `Kind` and `kind` — a shim here would outlive the reason for it and hide the next casing bug.

**The open decision: may ports be computed from the node's own parameters?** n8n allows it — `INodeTypeDescription.inputs` is typed `Array<NodeConnectionType | INodeInputConfiguration> | ExpressionString`, where `ExpressionString` is the template literal type `` `={{${string}}}` `` — and the AI Agent relies on it for conditional slots. KilasFlow needs the capability but should not adopt the mechanism. Recommend declaring a port conditionally instead: a port carries the same condition set the property-visibility ticket defines, evaluated against the node's own parameters. A computed port stays data, the compiler can evaluate it with no expression engine, the editor evaluates it with the same shared rule it already uses for properties, and there is no path by which a node definition becomes executable text. The cost is that a port count cannot be arbitrary arithmetic — which is a cost worth paying, and which p3-4's WAHA Trigger works around anyway by declaring a fixed output table per typeVersion.

One thing this ticket does not do: it does not model cluster-node semantics, slot rules or the direction inversion in n8n's AI connection JSON. That is p5-1's work, built on the kinds and descriptors this ticket delivers.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, section "p2 — Node metadata foundation", entry V2-p2-8.
- `internal/workflow/compiler.go` — `Port`, `NodeDefinition`, `IREdge`, the per-edge port checks and `ErrorCode` values.
- `internal/workflow/document.go` — `ConnectionKind`, its four constants, `knownConnectionKind`.
- `internal/node/registry.go` — the duplicated `knownConnectionKind` and `validatePorts`.
- `web/src/lib/api/generated/models/port.ts` — the generated `{Kind, Name}` shape proving the missing tags.
- `web/src/lib/workflow-editor/ports.ts`, `web/src/lib/workflow-editor/node-visual.ts` — every SPA reader of `port.Kind`.
- n8n 2.34.0 reference (read-only, outside this repo): `packages/workflow/src/interfaces.ts` — `NodeConnectionTypes`, `INodeInputConfiguration`, `INodeOutputConfiguration`, `INodeFilter`, `ExpressionString`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 02 — diamond sub-node handles rendered below the node, dashed arrowless edges, and the red asterisk marking a required slot — port metadata this phase adds. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
