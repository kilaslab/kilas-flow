---
id: FEAT-pn3dtq
title: Add secure credentials, safe expressions, and HTTP Request node
status: todo
priority: critical
labels:
    - credentials
    - expressions
    - http
    - security
deps:
    - FEAT-3mady6
    - FEAT-f681vt
parent: EPIC-c7gbdp
phase: p2
created: "2026-08-29T15:41:06Z"
updated: "2026-08-29T15:41:06Z"
---

## Scope

Complete the first end-to-end automation slice safely: encrypted credential management, a non-JavaScript expression subset, and an HTTP Request node. Reuse the core registry/runtime/editor instead of creating special paths.

## Acceptance criteria

- Credential CRUD stores encrypted payloads using a configured master key; plaintext credential values never enter workflow JSON, API list responses, logs, or execution records.
- Credential ownership/type and allowed HTTP-domain scope are validated before node execution. The HTTP node has request/payload/time limits and an SSRF policy that rejects disallowed private/internal targets according to documented policy.
- Fixed and expression parameter values are represented explicitly; the evaluator supports the approved V1 context (`$json`, `$input`, `$node`, `$env`, `$execution`) and rejects arbitrary JavaScript/code execution.
- HTTP Request is a registered node with metadata-driven parameter UI, credential selection, expression preview/error feedback, and deterministic request/response item mapping.
- A running end-to-end test proves Manual Trigger → Set → HTTP Request, including expression resolution, success/error persistence, redaction, credential non-disclosure, and SSRF-denial coverage.

## References

- PRD: §§24 Core nodes, 33–34, 36 Credentials, 49–50, 57; Milestone 2 and §65 first slice.
- Design reference: `11-ndv-http-request-params.png`, `12-canvas-wired-manual-set-http.png`, `15-param-fixed-expression-toggle.png`, `16-expression-editor.png`, `18-credentials-list.png`, `22-credential-modal-basic-auth.png`, `23-credential-saved.png`.

## Relevant documentation

- Consult current Go crypto and HTTP guidance only through their official docs if a non-standard API/library is chosen.
- Use `find-docs` before library-specific SSRF, encryption, request-client, or Svelte Flow API decisions; record links/versions in work evidence.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.
- `web-design-guidelines`, `playwright-cli` for the credential/editor browser flows.
