---
id: FEAT-9ns8cr
title: Stream standardized execution events to the live inspector
status: todo
priority: high
labels:
    - execution
    - realtime
    - sse
    - observability
deps:
    - FEAT-0j7r5s
parent: EPIC-c7gbdp
phase: p2
created: "2026-08-29T15:42:54Z"
updated: "2026-08-29T15:42:54Z"
---

## Scope

Standardize execution events independently from any specific node type and expose a secure live feed for the inspector. This is the common channel that later supports node progress, webhook processing, and AI token/tool traces.

## Acceptance criteria

- A documented internal event contract covers execution/node started, output, completed, failed, cancelled, and workflow saved events with stable execution/workflow/node correlation IDs.
- The runtime publishes events without making event delivery a prerequisite for durable execution/node-run persistence or successful workflow completion.
- `GET /api/v1/executions/:id/events` provides an authenticated, tenant-scoped live stream with reconnect/terminal-state behaviour defined and tested.
- The execution inspector receives and applies node/execution status updates in real time without allowing any workflow mutation or exposing unredacted event payloads.
- Backpressure, client disconnect, execution cancellation, and replay-after-refresh behaviour are covered by automated tests.

## References

- PRD: §§50–52.
- Design reference: `19-execution-detail-inspector.png`, `27-execution-logs-panel.png`, `29-logs-llm-call-tokens.png`.

## Relevant documentation

- Use `find-docs` before choosing or configuring an SSE/WebSocket/event-stream library. Prefer documented standard-library behaviour where it meets requirements; record the protocol/security sources used.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.
- `playwright-cli` for live-browser stream verification when applicable.
