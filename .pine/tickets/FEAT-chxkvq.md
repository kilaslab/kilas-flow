---
id: FEAT-chxkvq
title: Add constrained n8n workflow import and export
status: todo
priority: medium
labels:
    - interop
    - n8n
    - import
    - export
deps:
    - FEAT-6nh0wm
    - FEAT-vwzd6r
    - FEAT-6msqy3
    - FEAT-w3s12y
    - FEAT-900msn
parent: EPIC-c7gbdp
phase: p7
created: "2026-08-29T15:42:28Z"
updated: "2026-08-29T15:42:28Z"
---

## Scope

Implement interoperability as a boundary adapter after KilasFlow's canonical contract and supported native nodes are proven. Never execute arbitrary n8n JSON or acquire a runtime dependency on n8n packages.

## Acceptance criteria

- Import accepts only documented n8n workflow shape and maps supported nodes/connections into canonical KilasFlow JSON through a dedicated adapter.
- Supported mapping covers exactly the advertised node/version subset; unsupported nodes remain visible with actionable unsupported metadata and cannot silently execute as a different node.
- Import validates/compiles the mapped KilasFlow workflow before it can be activated or run; malformed/untrusted input cannot create executable unsafe behaviour.
- Export produces documented n8n-compatible JSON for supported canonical nodes. Round-trip fixtures identify intentional lossy fields and preserve supported graph semantics.
- Fixture tests cover normal linear, IF branching, webhook/HTTP, SQL, and unsupported-node workflows. User-facing import/export errors explain the exact unsupported element.

## References

- PRD: §§19–24, 42, 57; Milestone 7; Definition of Done item 17.
- Workflow shape references: `12-canvas-wired-manual-set-http.png`, `14-canvas-if-branching.png`, and `design-refs/n8n/INDEX.md` entries 12/14.

## Relevant documentation

- Use `find-docs` or official n8n documentation to verify the currently supported workflow JSON fields and node mapping assumptions. Record source URL, observed version, and every intentional compatibility limitation.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.
