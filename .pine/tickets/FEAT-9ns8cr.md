---
id: FEAT-9ns8cr
title: Stream standardized execution events to the live inspector
status: done
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
updated: "2026-09-05T01:50:00Z"
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

## Implementation Plan

- `internal/events` owns the contract: nine event types covering execution and node lifecycle plus `workflow.saved`, each carrying tenant, execution, workflow, and node correlation IDs so a consumer can join an event to its durable record without parsing the payload.
- The broker is in-process pub/sub with a bounded per-execution history for replay. Publish is non-blocking: a subscriber whose queue is full is marked lagged and the event is dropped for that subscriber only.
- `GET /api/v1/executions/{id}/events` is registered through huma's SSE support, so the stream appears in the OpenAPI document with one named event per type rather than an opaque message.
- The inspector folds live events over the durable trace it already fetched, then re-reads the trace once the stream ends.

## Work Evidence

- Delivery never gates persistence: every publish happens after the record it describes is durable, and a nil broker publishes nothing, so a run succeeds whether or not anyone is watching.
- Backpressure is proven rather than assumed: `TestPublishNeverBlocksOnASlowSubscriber` fires 50 events at a subscriber that is not reading and asserts the publisher completes and the subscriber is marked lagged. `TestExecutionEventStreamStopsWhenTheClientDisconnects` publishes 100 events after the client is gone and asserts the publisher does not block.
- Reconnect: `Last-Event-ID` resumes rather than restarts, verified in Go and live — resuming a finished run from id 3 returned only ids 4 and 5.
- Terminal behaviour: the server closes the stream after a terminal event, so a browser stops reconnecting to a run that already finished. Covered by `TestExecutionEventStreamReplaysRetainedHistoryThenClosesOnTerminal`.
- Tenant scoping: the broker keys streams by (tenant, execution), so guessing another tenant's execution ID yields an empty feed. Covered at both the broker and HTTP layers.
- Redaction: event payloads run through the same `execution.Redact` boundary. A live run of Manual → Set → HTTP Request streamed the HTTP node's response as `"apiKey":"[redacted]"`.
- Replay after refresh: retention is bounded per execution (`TestRetainedHistoryIsBoundedPerExecution`), and subscribing from 0 replays the whole retained history so a page opened mid-run or after it finished is still correct.
- The whole suite passes under `go test ./... -race`, plus `go vet ./...`, `pnpm test` (45 passing), `pnpm check`, `pnpm generate:api:check`, `pnpm build`, and `make smoke-sqlite`. Browser-verified: the inspector showed all three nodes succeeded with no credential material on the page.
