---
id: FEAT-zgm5s6
title: Publish host SDK for workflow and embed integration
status: done
priority: medium
labels:
    - sdk
    - embed
    - api
deps:
    - FEAT-900msn
    - FEAT-9ns8cr
parent: EPIC-c7gbdp
phase: p6
created: "2026-08-29T15:43:04Z"
updated: "2026-09-05T03:05:00Z"
---

## Scope

Package the stable public integration surface for host SaaS products: workflow management calls, embed-session creation/mounting, and typed execution events. The SDK is a convenience layer over the documented API, not an alternate source of state or authorization.

## Acceptance criteria

- A versioned SDK exposes typed workflow CRUD/lifecycle methods, embed-session creation/mounting, and execution-event subscription for the supported API surface.
- SDK requests authenticate through explicit host-supplied configuration; browser bundles do not require or embed privileged server API keys.
- Iframe mounting uses the verified embed session/origin/postMessage protocol and cleans up listeners/resources on unmount.
- Public TypeScript types and examples are generated or maintained from the API contract without manually divergent endpoint definitions.
- Package tests and a host-page integration example prove create workflow → mount embed → save/run (within scope) → receive execution event.

## References

- PRD: §§3.1–3.2, 36, 39–40, 51; Milestone 6.
- Design reference: `12-canvas-wired-manual-set-http.png` and `19-execution-detail-inspector.png` for the editor/execution capabilities surfaced by the host, not SDK visual design.

## Relevant documentation

- Use `find-docs` for any TypeScript package/build, iframe, or event-stream client library APIs selected. Record the official docs and public compatibility policy.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `verification-before-completion`.
- `playwright-cli` for the host/iframe integration smoke test.

## Implementation Plan

- `sdk/` is a versioned TypeScript package with two deliberately separate entry points: `@kilasflow/sdk/server` carries the host's credentials and belongs on a backend; `@kilasflow/sdk/browser` needs none and mounts the editor. Nothing imports across the boundary.
- Types are generated from the server's own OpenAPI document. The spec dump moved to `scripts/openapi-spec.mjs` so the web app and the SDK generate from one contract instead of two.

## Work Evidence

- No privileged key in a browser bundle by construction: `browser.ts` imports nothing from `server.ts`, so a bundle built from `/browser` has no import path to the credential-carrying client. Authentication is always explicit host configuration — a plain `headers` record rather than a dedicated `apiKey` field, because deployments authenticate differently and inventing one shape would force the others to work around it. `TestClientConfiguration` proves no header is sent when the host configured none.
- Typed surface: workflow CRUD, lifecycle, versions, runs; execution list/get/cancel and the live event stream; credentials and schedules; embed-session creation. Every method targets a documented endpoint, asserted by comparing the actual method+path of each call.
- Mounting uses the verified protocol: the editor announces itself, and only then is the token posted — to the editor's exact origin, never `'*'`. Messages are checked against `event.origin` *and* against the frame they came from. Both rejections are covered by tests, as is an iframe sandbox that permits scripts and same-origin but not top-level navigation or popups.
- Cleanup is proven, not claimed: after `unmount()`, the iframe is gone, a later message produces no callback, and the handshake timer never fires. `subscribeExecutionEvents` closes itself on a terminal event, removes every listener, and its unsubscribe is safe to call twice.
- One source of truth for types: `pnpm generate:types` writes `src/generated/models.ts` from a real server's OpenAPI document, and `pnpm generate:types:check` regenerates and fails on any difference, so a contract change cannot land without the SDK following it. No endpoint shape is written by hand.
- `examples/host-page` is a complete runnable integration whose split is the point: `server.mjs` holds the API credential, `index.html` holds none.
- Live end-to-end through the *built* SDK against a running server: created a workflow, minted an embed session (`kfe1.` token, correct scopes and origin, and asserted the response carries no API key), ran the workflow, read the live feed and received `execution.started → node.completed → node.completed → execution.completed`, then confirmed `succeeded` with two node runs and one history item.
- A real API improvement came out of testing: `createEmbedSession` validated synchronously, so a bad argument threw past a caller's `.catch()`. It is now async and rejects.
- `pnpm test` (23 passing across Node and jsdom), `pnpm check`, `pnpm build`, `pnpm generate:types:check`, plus the full repo suite: `go test ./...`, `go vet ./...`, `pnpm test`, `pnpm check`, `pnpm generate:api:check`, `pnpm build`, `make smoke-sqlite`.
