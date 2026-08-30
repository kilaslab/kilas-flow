---
id: FEAT-0j7r5s
title: Expose execution history and read-only graph inspector
status: todo
priority: high
labels:
    - execution
    - observability
    - ui
deps:
    - FEAT-3mady6
    - FEAT-f681vt
parent: EPIC-c7gbdp
phase: p1
created: "2026-08-29T15:40:52Z"
updated: "2026-08-29T15:40:52Z"
---

## Scope

Turn persisted execution/node-run records into useful REST endpoints and a read-only inspector that reuses the editor canvas. Establish the redaction boundary now, before webhooks, credentials, HTTP, and AI can leak sensitive data.

## Acceptance criteria

- `GET /api/v1/executions` supports documented filtering/pagination and `GET /api/v1/executions/:id` returns execution status, timestamps, node runs, and only authorized/redacted input/output/error data.
- Persisted execution data redacts known authorization/cookie/secret fields before storage and before API serialization; tests prove Basic/Bearer-style headers never appear in plaintext.
- `/executions` shows real status, start time, duration, and execution ID; `/executions/:id` displays a visibly read-only replay canvas with node status badges and edge item counts where available.
- Selecting a node shows input/output and resolved data in a safe JSON-oriented inspector; missing/failed/cancelled node runs are distinguishable.
- The normal editor and inspector share rendering primitives but cannot accidentally mutate a workflow from the execution view.

## References

- PRD: §§35–36, 50–52, 57; Definition of Done item 9.
- Design reference: `17-executions-list.png`, `19-execution-detail-inspector.png`, `20-execution-node-data-io.png`, `30-logs-details-tab.png`.
- Security observation in `design-refs/n8n/INDEX.md`: inbound Authorization headers must be redacted before execution persistence.

## Relevant documentation

- Refresh current Svelte Flow controlled-node/edge and rendering APIs via `find-docs` if needed for read-only state annotations.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.
- `web-design-guidelines`, `playwright-cli` when auditing UI and browser behaviour.
