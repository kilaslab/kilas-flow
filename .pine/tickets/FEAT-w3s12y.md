---
id: FEAT-w3s12y
title: Add isolated Go Code node with compiled WASM artifacts
status: todo
priority: high
labels:
    - code
    - wasm
    - security
deps:
    - FEAT-6msqy3
parent: EPIC-c7gbdp
phase: p5
created: "2026-08-29T15:41:58Z"
updated: "2026-08-29T15:41:58Z"
---

## Scope

Implement the restricted Go Code node as a secure, cacheable WASM execution path. Compilation is an artifact lifecycle, not part of every workflow run; runtime isolation is mandatory.

## Acceptance criteria

- A registered Go Code node provides an editor/property form, source validation, stable source hash, compilation status/error, and artifact reference without storing plaintext secrets in artifacts or logs.
- Source compilation occurs when code changes, not on every workflow execution; artifacts are cached and invalidated by source/runtime version as documented.
- The WASM runtime enforces CPU/time, memory, and host capability limits; user code cannot access arbitrary filesystem, network, process, environment, or KilasFlow internal database resources.
- Execution accepts/returns the documented item data shape and persists structured/redacted node-run errors.
- Automated tests cover successful execution, compilation failure, cache reuse/invalidation, timeout, memory/capability denial, and cancellation.

## References

- PRD: §§30–32, 35, 50, 57; Milestone 5; Definition of Done item 16.
- Design reference: `03-ndv-ai-agent.png` and `04-ndv-node-settings-tab.png` for generic node-panel/settings layout only.

## Relevant documentation

- Use `find-docs` to retrieve the current wazero API/security documentation and any compiler toolchain documentation before selecting APIs. Record precise versions and sandbox assumptions in the ticket.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.
