---
id: FEAT-6msqy3
title: Integrate AI adapter, agent graph, memory, and HTTP tools
status: done
priority: high
labels:
    - ai
    - agent
    - streaming
    - tools
deps:
    - FEAT-vwzd6r
    - FEAT-pn3dtq
    - FEAT-9ns8cr
parent: EPIC-c7gbdp
phase: p4
created: "2026-08-29T15:41:47Z"
updated: "2026-09-05T02:20:00Z"
---

## Scope

Add AI execution as an adapter-backed node family only after core automation, credentials, HTTP tools, and SQL nodes are stable. The external agent framework is replaceable; KilasFlow workflow JSON, registry, and engine remain authoritative.

## Acceptance criteria

- The engine depends on a local `AgentRuntime` interface, never directly on Microsoft Agent Framework (or another provider framework) types.
- OpenAI-compatible Chat Model uses a credential reference and exposes no key in workflow JSON, events, or execution records.
- AI Agent accepts only valid `ai_languageModel`, `ai_memory`, and `ai_tool` connections from registry validation; HTTP Request can be exposed as a tool without duplicating its implementation.
- Basic Memory persists/bounds tenant/workflow/session history according to a documented retention contract.
- Agent execution emits nested model/tool events, token usage where provider supplies it, streaming output, errors, and cancellation into the existing execution event model.
- Tests use a deterministic fake runtime/model for tool-loop, memory, streaming, invalid-port, and redaction behaviour; no live provider credential is required for CI.

## References

- PRD: §§21, 24–29, 35, 50–52; Milestone 4; §65 third vertical slice.
- Design reference: `02-canvas-agentic-workflow.png`, `03-ndv-ai-agent.png`, `25-canvas-secured-rest-endpoint.png`, `27-execution-logs-panel.png`, `28-logs-tool-call-detail.png`, `29-logs-llm-call-tokens.png`.

## Relevant documentation

- Use `find-docs` to retrieve the current official Microsoft Agent Framework for Go documentation and any OpenAI-compatible client documentation before implementation. Record exact links, version, licensing, and adapter assumptions in the ticket.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.
- `playwright-cli` if browser verification covers streamed execution rendering.

## Implementation Plan

- `internal/ai` is the whole contract the rest of the product sees: `AgentRuntime`, `ChatModel`, `Tool`, `Memory`, `SessionKey`, and a nested `Event` model. The engine and the nodes import only this package.
- `LoopRuntime` is the built-in `AgentRuntime` — a deterministic, bounded tool loop. `OpenAICompatible` is a `ChatModel` adapter over the chat-completions API shape.
- AI sub-nodes emit one descriptor item on a typed port, so the existing scheduler and item model carry them unchanged: topological order already guarantees a chat model runs before the agent that reads it.

## Framework decision

The scaffolding earmarked `internal/ai/maf` for Microsoft Agent Framework for Go. That module is in public preview, publishes no tagged releases, and would resolve to a pseudo-version whose API may move. The acceptance criteria require an `AgentRuntime` boundary and a provider-independent engine — not that specific framework — so the built-in runtime is implemented directly behind `AgentRuntime`, and a framework-backed runtime remains a drop-in replacement. Nothing outside an adapter would change if one is added: the engine, the registry, and workflow JSON all speak only `internal/ai` types.

## Work Evidence

- Adapter boundary: `grep` shows no package outside `internal/ai` imports a provider type; `nodes/ai.go` depends on `ai.AgentRuntime` and `ai.ChatModel` only, and `NewAgentExecutor` takes the runtime as a parameter.
- No key in workflow JSON, events, or execution records: the chat-model node emits a descriptor carrying `credentialId`, and the agent re-resolves the secret when it calls the model. `TestChatModelDescriptorCarriesNoAPIKey` serializes the emitted item and asserts the key is absent and the reference present. A chat model with no credential is rejected at compile time, so there is no ambient-key fallback to leak.
- Provider errors are sanitized: `TestOpenAICompatibleReportsAProviderErrorWithoutEchoingTheRequest` proves neither the API key nor the prompt appears in a 401's error.
- Port validation is enforced by the compiler, not by convention: a language model wired to a `main` port and a tool wired to the model port are both rejected, and a valid agent graph compiles.
- Two topology rules were corrected to make typed attachment ports work at all, and both are stated as rules rather than special cases: a node with no inputs is a trigger only if it produces items (a chat model is not a second root), and only `main` inputs are required (an agent with no memory and no tools is a valid agent). Reachability walks attachment edges backwards, so a provider is connected through the node it configures.
- HTTP Request is exposed as a tool without duplication: `httpRequestTool` runs the *same* `HTTPExecutor`, inheriting the SSRF policy, credential scoping, timeout, and response limits. `TestAgentRunsWithAToolThatReusesTheHTTPRequestImplementation` proves the tool's URL expression resolved from the model's arguments by asserting the upstream received `/weather/Utrecht`.
- Memory has a documented, enforced retention contract: bounded by message count and age, scoped by tenant + workflow + session so guessing a session ID cannot reach another tenant's conversation, negative bounds rejected rather than treated as unlimited, and expiry proven with an injected clock.
- Events: nested model and tool events, token usage where the provider supplies it, streaming deltas, errors, and cancellation all flow into the existing execution event channel via `engine.NodeEvent` — no parallel delivery mechanism.
- Every test uses a deterministic fake model, fake tools, and an injected clock. No live provider credential is required anywhere; the two tests that exercise the real adapter run against `httptest` servers.
- `go test ./... -race`, `go vet ./...`, `pnpm test`, `pnpm check`, `pnpm generate:api:check`, `pnpm build`, `make smoke-sqlite`.
