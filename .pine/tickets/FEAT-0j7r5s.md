---
id: FEAT-0j7r5s
title: Expose execution history and read-only graph inspector
status: done
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
updated: "2026-09-05T01:25:00Z"
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

## Implementation Plan

### Redaction boundary

- Add `internal/execution/redact.go`: `Redact(json.RawMessage) json.RawMessage` deep-walks any JSON value and replaces the value of every object key whose normalized name (lowercased, `-`/`_`/space removed) is sensitive — authorization, cookie/set-cookie, api key/secret, token family, password, private key, credential(s), session, signature — with `"[redacted]"`. It also redacts any string that reads as a `Basic`/`Bearer`/`Digest` credential regardless of its key, because header data also arrives as `{name, value}` pairs where the key is `value`.
- Apply it once at the repository boundary, in the execution store's payload helper, so `Create`, `QueueManualLatest`, `UpdateRuntime`, and `CreateNodeRun` are all covered, and again when building API resources as defence in depth. Tests must prove Basic/Bearer header material never reaches storage or the wire in plaintext.

### Listing endpoint

- `repository.ExecutionFilter{WorkflowID, Statuses, Trigger, Limit, Cursor}` plus `ExecutionPage{Records, NextCursor}`; keyset pagination ordered by `started_at DESC, id DESC` with an opaque cursor, so a new execution cannot shift a page. Summaries carry no node runs.
- `GET /api/v1/executions` accepts `workflowId`, repeatable `status`, `trigger`, `limit` (1–100, default 25) and `cursor`, and returns `{items, nextCursor}`. The handler reads through the repository; the live controller stays for get/cancel.

### Read-only inspector

- `/executions`: status badge, workflow, started time, duration, execution ID, status filter, cursor "Load more".
- `/executions/[id]`: replay canvas built from the pinned workflow revision and reusing `documentFromCanvas` plus the editor's canvas node, with `nodesDraggable`, `nodesConnectable`, and `edgesReconnectable` off so the execution view cannot mutate a workflow. Node status badges come from the node-run trace; edges show item counts where the trace has them; selecting a node opens a JSON input/output/error inspector that distinguishes missing, failed, and cancelled runs.

## Work Evidence

- `internal/execution/redact.go` redacts credential material by normalized key name and by credential scheme, and is applied in the execution store's `payload` helper so every write path is covered by construction, plus again in the API resource mappers.
- Redaction is proven twice: `TestRedact*` covers the rule, and `TestExecutionStoreRedactsCredentialsBeforeStorage` reads the SQLite columns back directly to prove Basic/Bearer, cookie, and API-key material never lands in storage. `TestExecutionAPIRedactsCredentialsInResponses` proves the same for the wire.
- `GET /api/v1/executions` filters by workflow, status, and trigger, pages with an opaque keyset cursor, and reports `durationMs`. A cursor the client never received is a 400 and an unknown status is a 422, not a 500.
- Added `GET /api/v1/workflows/{id}/versions/{versionId}`: executions pin a version ID, so the inspector replays the exact revision that ran rather than the workflow's current draft. A version ID belonging to another workflow is a 404.
- Browser-verified: `/executions` lists status, workflow name, start time, duration, trigger, and execution ID with status/workflow filters; `/executions/:id` renders the pinned graph with per-node status badges, a "1 item" edge count, and a node inspector showing input/output. Dragging a node leaves its transform unchanged and its handles carry no `connectable` class, so the replay cannot mutate a workflow.
- `go test ./...`, `go vet ./...`, `pnpm test` (32 passing), `pnpm check` (0 errors), `pnpm generate:api:check`, `pnpm build`, `make smoke-sqlite`.
