---
id: FEAT-96p7m3
title: Add the Basic LLM Chain node
status: todo
priority: high
labels:
    - ai
    - parity
deps:
    - FEAT-ybm2pd
parent: EPIC-m42s3g
phase: p5
created: "2026-09-05T05:01:00Z"
updated: "2026-09-05T05:01:00Z"
---

## Scope

KilasFlow's AI family is exactly four node types — `kilasflow.chatModel`, `kilasflow.memoryBuffer`, `kilasflow.httpTool` and `kilasflow.agent`, all registered in `nodes/ai.go`. There is no chain. The Basic LLM Chain is the second most-used node in n8n's LangChain pack after the Agent, and it is not an agent wearing a smaller hat: it takes a model and an optional output parser, it has no memory slot and no tools, and it makes one model call per item with no loop.

That "no memory slot" is a parity fact, not an omission to be improved on. n8n's Basic LLM Chain deliberately has no `ai_memory` input; a user who wires memory expects the Agent. Adding a slot KilasFlow's version has and n8n's does not would make an exported workflow unrepresentable and an imported one ambiguous.

The prompt surface is the part that needs new machinery. n8n's chain takes either the text of a named field on the incoming item, or a `messages.messageValues` fixedCollection of typed rows — System, AI and Human templates in order. `internal/node/registry.go` knows six property kinds (`string`, `number`, `boolean`, `select`, `keyValue`, `conditions`) and none of them can express a repeating group of typed rows, so this node depends on p2-2 adding `fixedCollection`.

## Acceptance criteria

- [ ] A Basic LLM Chain node registers with a `main` input, a `main` output, an `ai_languageModel` slot capped at one, an optional `ai_outputParser` slot — and no memory slot.
- [ ] Both prompt modes work: take the text from a named field of the incoming item, or define an ordered message list.
- [ ] The message list is a fixedCollection of typed rows whose text supports expressions, rendered by the generic property form with no chain-specific UI component.
- [ ] Each incoming item produces exactly one output item carrying the model's text, or the parsed structure when an output parser is attached.
- [ ] Exactly one model call happens per item when no parser retry is needed, proven by the recorded `ai.model.*` events rather than asserted.
- [ ] An empty message list, or a row with empty text, fails validation with a message naming the row.
- [ ] The node appears in the editor with its own icon and accent, not the fallback grey tile.

## Implementation Plan

Register the definition in `nodes/ai.go` beside the agent, and its executor in `nodes/executors.go` under a new `core.chainLlm` id — the registration map there is the single place executors are bound, and `internal/node/registry.go` will accept a definition whose `ExecutorID` has no executor behind it, so a missing entry compiles, passes every test and fails at run time.

Drive the chain from `ai.ChatModel` directly, not `ai.AgentRuntime`. A chain is one `Complete` or `Stream` call; routing it through `LoopRuntime` would hand it a tool loop it must not have, and would make the "one model call per item" criterion untestable.

Model resolution is the same three steps the agent already performs in `AgentExecutor.Execute`: read the descriptor from the typed port, pull `credentialId` out of it, and re-resolve the secret through `request.Credentials.ResolveCredential` before constructing `ai.NewOpenAICompatible` with the policy-bound client. Factor that out of `AgentExecutor.Execute` into a shared helper rather than copying it — the descriptor carries the credential id and never the key precisely because it is persisted into the execution record, and a copy is where that invariant gets lost.

The trap is `descriptorFrom`. It scans items for the `$ai` key, and the chain has a `main` input as well as a model slot, so reading the model descriptor from `input["main"]` will find nothing on a well-formed graph and something surprising on a malformed one. Read it from the model port by name.

Until p2-5 makes node identity server-driven, the editor's `ICONS` map in `web/src/lib/workflow-editor/node-visual.ts` is hardcoded and a type with no entry renders as a grey `Box`; add the entry in the same commit.

One decision. The "take from field" prompt mode could be deferred to keep the first cut to the message list alone. Recommend implementing both: "take from field" is n8n's default, so it is the shape most imported chains actually carry, and a chain that imports into an empty prompt is worse than no chain at all.

## References

- Roadmap plan, p5 section, entry V2-p5-3: `.pine/roadmap.md`.
- `.pine/roadmap.md` — p2 entry V2-p2-2, the `fixedCollection` property kind.
- `nodes/ai.go`, `nodes/executors.go` (`RegisterExecutors`), `internal/node/registry.go` (`PropertyKind`), `internal/ai/ai.go` (`ChatModel`), `web/src/lib/workflow-editor/node-visual.ts`.
