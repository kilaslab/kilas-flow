---
id: FEAT-zgm5s6
title: Publish host SDK for workflow and embed integration
status: todo
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
updated: "2026-08-29T15:43:04Z"
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
