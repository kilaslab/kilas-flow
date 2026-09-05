---
id: FEAT-xr7ga9
title: Prove the AI agent path end to end against a local model
status: todo
priority: high
labels:
    - e2e
    - testing
    - ai
deps:
    - FEAT-cx3hq1
    - FEAT-kwxxd0
    - FEAT-cgm1y3
    - FEAT-je4f4t
parent: EPIC-m42s3g
phase: p11
created: "2026-09-05T12:05:21Z"
updated: "2026-09-05T12:05:21Z"
---

## Scope

The AI cluster is the most-changed and least-verifiable part of the product. p5 delivers chat models, the cluster-node model for sub-nodes, the agent at n8n parity, memory with session semantics, the tool family and `$fromAI`, and a structured output parser. Every one of those is verified by Go tests against a stubbed model, and none of them is verified against a model that actually decides something.

That distinction matters here more than it usually would, because the agent's correctness *is* its interaction with a model. A stub returns whatever the test author expected; it cannot produce a malformed tool call, an unexpected argument shape, a refusal, or a second tool call when one was anticipated. The behaviours worth testing are exactly the ones a stub cannot generate.

V2-p11-2 supplies the runtime — a local Ollama serving an OpenAI-compatible API that the existing `baseUrl` parameter already reaches, behind an explicit `allowed_hosts` entry with the SSRF guard left on. This ticket spends it.

What needs proving through the editor and a real execution:

- **The cluster wiring.** A chat model, a memory and one or more tools connect to an agent through the typed AI ports — `ai_languageModel`, `ai_memory`, `ai_tool`, with the exact casing the format demands. A sub-node connected to the wrong port, or an agent with no model attached, must fail at save with a message rather than at run time.
- **A tool actually being called.** The agent decides; the tool runs; its result returns to the agent; the agent produces a reply. That whole loop, observed through the execution's node runs.
- **Memory across turns.** Session semantics with `fromInput` and `customKey`, and the auto-scoping suffix — plus the property that two sessions do not see each other's history, which is a tenant-isolation concern as much as a feature.
- **Streaming.** `stream` defaults to true on the chat model node, and the execution event stream is how a user sees output arrive. A test that only checks the final record never exercises the streaming path at all.
- **The trigger-to-agent-to-reply shape.** The epic's own first proof is a Telegram bot answering through an AI agent, and it is the smallest complete demonstration of what this product is for.

One redaction interaction is worth testing explicitly rather than trusting. `internal/execution/redact.go` runs at webhook ingest, and `session` and `sessionid` are on the sensitive-key list — which is why V2-p1-6 exists, since n8n-style AI memory keyed on `sessionId` would otherwise collapse every user into one bucket. An end-to-end memory test with two distinct sessions is the test that proves that fix held.

## Acceptance criteria

- [ ] An agent with a chat model, a memory and a tool attached runs to completion against the local model, driven from the editor and observed through the execution event stream.
- [ ] A tool invocation is asserted structurally — that the tool node ran, with arguments of the expected shape, and that its result reached the agent — never by matching generated prose.
- [ ] Two conversations with different session keys keep separate histories, and a test proves neither sees the other's messages.
- [ ] Streaming output arrives as execution events during the run rather than only in the final record.
- [ ] Connecting a sub-node to the wrong port kind, or leaving an agent without a language model, is refused at save with a message naming the problem.
- [ ] A tool that fails, and a model that returns a malformed tool call, each leave the execution in a defined state with a diagnostic rather than a hang or a partial record.
- [ ] The complete trigger-to-agent-to-reply path runs end to end, matching the epic's first acceptance proof, with no Node.js process involved.
- [ ] The suite runs against `gemma4:12b-mlx` and skips with a clear message when that model is unavailable, never silently passing by falling back to a stub.

## Implementation Plan

Depend on V2-p11-2 for the runtime and on the p5 tickets for the nodes; do not begin before the cluster-node model lands, because the wiring is the thing under test and it does not exist yet.

The model is `gemma4:12b-mlx`, pinned by V2-p11-2 on the owner's instruction. Confirm that ticket's tool-calling check passed before starting: this entire suite is a tool-calling suite, and a model that chats fluently without emitting OpenAI-format tool calls makes every assertion below unwritable.

Design every assertion to survive a model that is not deterministic in its wording. Temperature zero and a pinned model tag get consistency of behaviour, not of text. Assert that a tool ran, that an argument parsed, that an execution completed, that two sessions differ. Never assert a sentence. The single biggest risk to this suite is that it is written with text assertions, fails on the first model update, and gets disabled.

Give the tools deterministic behaviour so the agent's decision is the only variable. A tool that returns a fixed value for a fixed input makes "did the agent call it with the right argument" a clean assertion; a tool that itself calls a third party adds a second source of nondeterminism to a test that already has one.

For the malformed-tool-call case, do not try to coax a real model into producing one — that is unreliable and slow. Use the stub model for that specific case and say so in the test. The rule is that the *loop* is proven against a real model and the *error paths* may be proven against a stub, because an error path's value is that the code handles a shape, not that a model produced it.

The memory-isolation test should drive two sessions through the webhook path rather than through direct execution, because that is the path where redaction runs and where the `sessionId` collapse would occur. A memory test that bypasses ingest tests the memory store and not the defect.

One thing to state plainly in the suite's own documentation: a green run proves the wiring, not the quality of agent output. Nobody should read this suite as evidence that agents behave well, and the distinction should be written where a reader of a passing pipeline will see it.

## References

- Roadmap plan, p11 section, entry V2-p11-6: `.pine/roadmap.md`.
- `.pine/tickets/FEAT-kwxxd0.md` — V2-p11-2, the local runtime this spends.
- `.pine/tickets/FEAT-ybm2pd.md` — V2-p5-2, the cluster-node model whose wiring is under test.
- `.pine/tickets/FEAT-cgm1y3.md` — V2-p5-3, the agent at n8n parity.
- `.pine/tickets/FEAT-je4f4t.md` — V2-p5-6, the tool family and `$fromAI`.
- `.pine/tickets/FEAT-096vs9.md` — V2-p5-5, memory session semantics.
- `nodes/ai.go` — `chatModelNode()`, the `stream` default, and the typed AI ports.
- `internal/ai/openai.go` — the chat completions path and streaming.
- `internal/execution/redact.go` — the sensitive-key list including `session` and `sessionid`.
- `internal/api/handlers/executions.go` — the SSE events the streaming assertions read.
- `.pine/tickets/EPIC-m42s3g.md` — the Telegram-agent-reply proof this suite makes executable.
- Owner instruction, 2026-09-05: `gemma4:12b-mlx` is the model for the end-to-end Playwright suites.
