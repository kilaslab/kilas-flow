---
id: FEAT-yx0qt6
title: Complete the KilasFlowClient operation surface
status: todo
priority: high
labels:
    - sdk
    - api
deps:
    - FEAT-bscygc
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:49:01Z"
updated: "2026-09-05T11:49:01Z"
---

## Scope

`KilasFlowClient` in `sdk/src/server.ts` exposes **16 methods against an API of 33 operations**. The half it covers is the half the embed demo needed; the half it omits is most of what a host application actually automates.

Present: `listWorkflows`, `getWorkflow`, `createWorkflow`, `updateWorkflow`, `deleteWorkflow`, `getWorkflowVersion`, `activateWorkflow`, `deactivateWorkflow`, `runWorkflow`, `listExecutions`, `getExecution`, `cancelExecution`, `executionEventsUrl`, `listCredentials`, `listSchedules`, `createEmbedSession`.

Absent entirely:

- **Credentials beyond listing** — `create-credential`, `get-credential`, `update-credential`, `delete-credential` and `test-credential`. The client can list credential names and cannot create one, so a host that provisions a tenant's integrations must drop to raw `fetch` for the most security-sensitive calls in the product. `test-credential` is the worst omission of the five: it is the only operation that tells a host whether a credential it just stored actually works.
- **Credential types** — `list-credential-types`, without which a host cannot render a credential form at all.
- **Schedules beyond listing** — `create-schedule`, `update-schedule`, `delete-schedule`.
- **The node catalogue** — `list-node-types`, `get-node-icon`, `load-node-property-options`, `get-expression-grammar`. A host building any workflow UI of its own has no typed path to the catalogue that UI must render.
- **n8n interop** — `import-workflow` and `export-workflow`, which is the migration path this whole programme exists to serve.
- **Liveness** — `get-health` and `get-ready`, which every deployment wrapper wants.

The client is hand-written against generated model types, and that is a deliberate and good decision — `sdk/orval.config.ts` generates types only, with the comment "Types only. The SDK writes its own request layer." The problem is not the hand-writing; it is that hand-writing has no feedback loop. `sdk/scripts/check-types.mjs` catches a *model* that drifts and says nothing about an *operation* that was never wrapped, so the gap is invisible to every check in the repository and grows silently each time a handler is added.

## Acceptance criteria

- [ ] Every operation the OpenAPI document declares has a corresponding client method, or an explicit, documented exclusion with a stated reason.
- [ ] Credential create, get, update, delete and test are all reachable through the client, and `test-credential` returns its `{ok, detail}` shape typed rather than as `unknown`.
- [ ] Schedule create, update and delete are reachable, completing the surface `listSchedules` starts.
- [ ] The node catalogue is reachable — node types, the icon route, dynamic option loading and the expression grammar — so a host can render its own workflow UI without raw `fetch`.
- [ ] Workflow import and export are reachable, and the import result's diagnostics and minted webhook URLs are typed rather than opaque.
- [ ] A test reads the OpenAPI document produced by `scripts/openapi-spec.mjs` and **fails when an operation exists that no client method covers and no exclusion list names**, so this gap cannot silently reopen.
- [ ] Methods added by this ticket follow the existing conventions exactly: `AbortSignal` last, `Promise<void>` for 204, RFC 9457 errors surfaced as `KilasFlowError`, no ambient credential lookup.
- [ ] The binary-returning icon route is handled honestly rather than forced into the JSON transport, and its content type and caching semantics are preserved.

## Implementation Plan

Do the coverage test first, before adding a single method. Written second it is a formality; written first it produces the exact list of what is missing, keeps producing it as the API grows, and turns this ticket from a judgement call into a checklist. It should read the generated document rather than a hand-maintained list, and its exclusion list should be short enough to review.

The transport is already right for almost everything. `Transport.request` sets `Accept: application/json`, adds `Content-Type` only when there is a body, maps 204 to `undefined`, parses `application/problem+json` into `KilasFlowError`, and combines a caller `AbortSignal` with a 30-second `AbortSignal.timeout`. Most new methods are one line each on top of it.

Two operations do not fit that mould and should not be bent into it.

`get-node-icon` returns an image with a strict CSP and a one-day immutable cache header, not JSON. Adding it to the JSON transport would either lose the content type or force the transport to grow a mode. Recommend the `executionEventsUrl` precedent instead: return a URL the caller uses directly, since a host rendering an icon wants a URL for an `<img>` far more often than it wants bytes.

`import-workflow` takes the raw n8n document as an opaque JSON value inside a typed envelope, and its response carries the `unsupported` diagnostics and the minted `webhooks` array. Type the envelope and the response fully; leave the n8n document itself `unknown`, because typing a foreign format the importer deliberately treats as untrusted input would be a false promise.

One boundary to respect while adding these. `internal/api/middleware/embed.go` denies `/workflows/import`, `activate`, `deactivate` and `DELETE` to embed sessions outright. The server client and the browser client are separate entry points for exactly this reason, and none of the operations added here belongs in `sdk/src/browser.ts`.

Do not add convenience layers in this ticket — no polling helper, no retry policy, no pagination auto-follow. The client's value is that it is a thin, predictable, typed shape over a documented API; V2-p10-7's cursor helper is the one exception and it is scoped there deliberately.

## References

- Roadmap plan, p10 section, entry V2-p10-5: `.pine/roadmap.md`.
- `sdk/src/server.ts` — `KilasFlowClient` and its 16 methods.
- `sdk/src/http.ts` — `Transport.request`, `KilasFlowError`, `ProblemDetail`, and the 204 and timeout handling every new method inherits.
- `sdk/orval.config.ts` — the types-only decision and its stated rationale.
- `sdk/scripts/check-types.mjs` — the model drift check, which cannot see a missing operation.
- `internal/api/routes.go` and `internal/api/handlers/` — the 33 registered operations, in `credentials.go`, `schedules.go`, `nodes.go`, `interop.go`, `workflows.go`, `executions.go`, `embed.go` and `system.go`.
- `internal/api/handlers/nodes.go` — the icon route's CSP and cache headers.
- `internal/api/handlers/interop.go` — the import and export envelopes, including `unsupported` and `webhooks`.
- `internal/api/middleware/embed.go` — `permits()`, which bounds what may ever appear in the browser entry point.
