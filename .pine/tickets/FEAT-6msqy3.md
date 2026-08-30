---
id: FEAT-6msqy3
title: Integrate AI adapter, agent graph, memory, and HTTP tools
status: todo
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
updated: "2026-08-29T15:41:47Z"
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
