---
id: FEAT-ybm2pd
title: Adopt the cluster-node model for AI sub-nodes
status: done
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
updated: "2026-09-06T00:00:00Z"
---

## Scope

KilasFlow already has a sub-node shape, but by accident rather than by design. `nodes/ai.go` gives the chat model, the memory and the HTTP tool a single typed output port each, and each emits one descriptor item under the key `$ai` (`descriptorKey`). The agent reads them back from `input["model"]`, `input["memory"]` and `input["tools"]`. That works, and topological order already guarantees a sub-node runs before the root that reads it — but nothing names the pattern and nothing enforces the cardinality n8n's editor and runtime both guarantee.

The cardinality gap is real and silent. `validateExecutableTopology` in `internal/workflow/compiler.go` builds an `incoming[nodeID][portName]` count and uses it only to check that a `main` port received *something*; every non-`main` port is skipped outright as optional, and there is no upper bound anywhere. On the client, `canConnect` in `web/src/lib/workflow-editor/ports.ts` requires only that the two port kinds match and that the exact edge does not already exist. So two chat models can be wired to one agent, the graph compiles, the workflow activates, and at run time `descriptorFrom` returns whichever descriptor it meets first — the model that actually runs is decided by item order.

This ticket adopts n8n's root/sub-node model explicitly, on top of the port descriptors and the widened `ConnectionKind` set that p2-8 introduces, and applies n8n's slot rules: `ai_languageModel`, `ai_memory` and `ai_outputParser` cap at one connection each, `ai_tool` is uncapped. n8n expresses exactly this through `INodeInputConfiguration` (`category`, `displayName`, `required`, `type`, `filter`, `maxConnections`) in `packages/workflow/src/interfaces.ts` of the reference checkout.

There is a direction trap that will bite anyone reasoning from the picture rather than the JSON. In an n8n workflow document the *sub-node* is the source key of the connection and the root agent is the target — the model connects to the agent, not the other way round. KilasFlow's `workflow.Connection` is also source-to-target, so the mapping is direct; the danger is that "the agent has a model" reads like the agent should be the source, and an importer written on that intuition produces a graph that compiles and never delivers a descriptor.

## Acceptance criteria

- [x] Every AI node registered by `nodes/ai.go` declares its slots through p2-8's port descriptors, with explicit cardinality: one connection each for `ai_languageModel`, `ai_memory` and `ai_outputParser`, uncapped for `ai_tool`.
- [x] The compiler refuses a graph that exceeds a slot's cap, naming the node, the port and the count, instead of compiling and choosing one at run time.
- [x] A slot declared required and left empty fails compilation with a message naming the slot; an optional empty slot still compiles, so an agent with no memory and no tools stays valid.
- [ ] The editor refuses the second connection into a capped slot while it is being dragged, so the conflict is visible before the draft is saved. **Not done — `web/` was held by another session throughout; see Work evidence.**
- [x] Descriptor delivery is deterministic: with N tools attached, the agent receives exactly N tool descriptors in a stable, tested order.
- [x] An n8n cluster imported with the sub-node as the connection source produces the same graph as one built natively, and an export puts the sub-node back on the source side.
- [x] A test proves that two chat models on one agent is a compile error, not a run-time coin flip.

## Implementation Plan

Start in `internal/workflow/compiler.go`. `validateExecutableTopology` already has the per-port incoming counts it needs, so the cap check is a few lines next to the existing "requires an incoming connection" check. While there, replace the blanket `if port.Kind != ConnectionMain { continue }` with the port descriptor's own `required` flag — that line is what currently makes every typed slot optional, and once ports can say so themselves the special case for `main` disappears.

Then `nodes/ai.go`: the four AI definitions declare their slots. Then the editor: `canConnect` in `web/src/lib/workflow-editor/ports.ts` gains a count of existing edges into the target port and compares it to the descriptor's cap.

The trap is the port wire format. `workflow.Port` in `internal/workflow/compiler.go` has no JSON tags, so it serializes with capital `Name` and `Kind`, and the SPA reads those exact keys — `web/src/lib/workflow-editor/ports.test.ts` and `node-visual.test.ts` are full of `{ Name: 'model', Kind: 'ai_languageModel' }`. Adding tags is a breaking client change and p2-8 owns that decision; do not make it here. Follow whatever casing p2-8 settled on, and regenerate the client with `pnpm generate:api` — `internal/node.Definition` is serialized directly as the `/api/v1/node-types` payload (`internal/api/handlers/nodes.go`), so any change to a port's shape is an OpenAPI change and CI drift checks fail otherwise.

With cardinality enforced upstream, `descriptorFrom` in `nodes/ai.go` can be tightened from "return the first match" to "assert exactly one", which turns the silent wrong-model bug into a loud one on any path that slipped past the compiler.

One design decision remains open. Sub-nodes could stay ordinary scheduled nodes that emit a descriptor item, as today, or become compile-time attachments the runner resolves without executing them. Recommend keeping the descriptor-item model: it works, it keeps topological order as the only scheduling rule, and it gives each sub-node its own row in the execution record. Revisit only when a sub-node must be invoked repeatedly inside a single agent run — a retriever, in p6 — because that is the case the current model genuinely cannot express.

## References

- Roadmap plan, p5 section, entry V2-p5-1: `.pine/roadmap.md`.
- `.pine/roadmap.md` — p2 entry V2-p2-8.
- `$KILASFLOW_N8N_REFERENCE/packages/workflow/src/interfaces.ts` — `NodeConnectionTypes` (13 values) and `INodeInputConfiguration`.
- `internal/workflow/document.go` (`ConnectionKind`, `knownConnectionKind`), `internal/workflow/compiler.go` (`Port`, `validateExecutableTopology`), `nodes/ai.go`, `web/src/lib/workflow-editor/ports.ts`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 02 — a real cluster node: one agent with Chat Model*, Memory and Tool slots and four sub-nodes attached. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Work evidence

Most of this ticket had already landed. The scope section describes a compiler with no cardinality enforcement at all — that premise is stale, and what was actually missing was the interop half.

### What was already there, verified against the files rather than the ticket

- `workflow.Port` (`internal/workflow/compiler.go`) already carries `Required`, `MaxConnections` and `AllowedNodeTypes`, all with JSON tags. The ticket's warning about untagged capitalised keys and a `pnpm generate:api` regeneration no longer applies; no exported type changed in this ticket, so there is no OpenAPI drift.
- `validatePortCardinality` already enforces both the cap and the required flag, with `ErrorPortFull` and `ErrorPortRequired` as separate codes. It runs beside `validateExecutableTopology` rather than inside it, so the `if port.Kind != ConnectionMain { continue }` line the plan asked to delete is now correct where it stands — required-ness is decided by the port descriptor one function over.
- `agentNode()` already declares `model` (required, max 1), `memory` (max 1) and `tools` (uncapped).
- The runner already sorts a node's incoming edges by port, source node, output index and edge ID before assembling `NodeInput`, so descriptor delivery was already deterministic — it was simply untested.
- Import and export already put the sub-node on the source side of a typed edge, tested by `TestImportKeepsAIConnections` and `TestAIConnectionsRoundTrip`.

### What was actually missing, and was built here

- **The interop AI type table (AC6).** `internal/interop/n8n` had no LangChain entry at all, so every AI node imported as `kilasflow.unsupported`: the wiring was right, the canvas looked right, and the workflow could never be activated because a placeholder deliberately fails compilation. Five mappings added — `agent`, `lmChatOpenAi`, `lmChatOpenRouter`, `memoryBufferWindow`, `toolHttpRequest` — with translators in both directions in `parameters.go`.
- **`descriptorFrom` tightened to `soleDescriptor`** in `nodes/ai.go`. It returned the first match, which made the model that actually runs a function of item order on any path that reached the runner without passing the cap check. It now refuses and names the slot.
- **Tests for the rules that were enforced but unpinned** — two chat models, a second memory, a missing required model, an agent with neither memory nor tools, and end-to-end tool ordering.

### Translation decisions worth knowing

- n8n splits the agent prompt into `promptType` and `text`, and keeps the system message and iteration bound inside an `options` collection. A field left at its default is never stored, so n8n's defaults are materialised on import — otherwise a required `prompt` or `sessionId` arrives empty and the import cannot compile.
- The OpenAI node stores its model as a resource locator from typeVersion 1.2 and the OpenRouter node stores a bare string at every published version, so export writes each provider its own shape.
- n8n's HTTP Request Tool has no tool-name parameter: the name a model calls is derived from the node's canvas name. The importer derives it the same way, so a system prompt that already named the tool still names the same tool.
- Output parser, fallback model, guardrails prompts and `{placeholder}` tool URLs are reported as blocking rather than dropped quietly, because each one changes how the agent answers.

### Not done

- **AC4, the editor check, is not in this change.** `web/` was held by another session for the whole of this ticket. `canConnect` in `web/src/lib/workflow-editor/ports.ts` still compares only port kinds, so a second edge into a capped slot can be drawn and is refused on save rather than during the drag. The server is the authority either way; this is a UX gap, not a correctness one.
- **No `ai_outputParser` slot was added to the agent.** `ConnectionOutputParser` exists in `ConnectionKinds()`, but no registered node emits it, so the slot would be permanently unfillable — a dead port on every agent. It belongs with the output parser node itself.

### Commands

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `gofmt -l .` outside `web/` — empty.
- `go test ./... -count=1` — all packages pass.
- `go test ./nodes/ -race -count=1` — one failure, `TestTheGoCodeNodeRunsOncePerItemWhenAsked`, which is the pre-existing BUG-9s3htg wasm compile ceiling. No new race failures.

### Failing-first proof

- `soleDescriptor` reverted to returning the first match: `TestASecondModelDescriptorIsRefusedRatherThanSilentlyPicked` fails — the agent runs on the first of two models and reports a credential error rather than naming the conflicting slot.
- The five mapping entries neutralised: `TestAnImportedAgentClusterCompilesInsteadOfArrivingAsPlaceholders`, `TestAnImportedClusterIsTheSameGraphAsOneBuiltNatively`, `TestAnImportedClusterArrivesConfigured`, `TestTheTwoChatModelProvidersKeepTheirOwnModelShape` and `TestTheClusterMappingMatchesWhatTheReferenceWasRecordedAsSaying` all fail, reporting every AI node as `kilasflow.unsupported`.

### Licence boundary

Reference facts were transcribed into `internal/interop/n8n/testdata/n8n_cluster_nodes.json` with the source files, the n8n version (2.34.0) and the read date, in the shape `nodes/testdata/n8n_chat_model_options.json` established. Nothing in the repository reads the reference checkout, and no n8n package was added to any manifest.
