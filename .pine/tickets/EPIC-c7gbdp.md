---
id: EPIC-c7gbdp
title: KilasFlow V1 — API-first workflow platform
status: todo
priority: high
labels:
    - roadmap
    - v1
    - api-first
phase: p0
created: "2026-08-29T15:39:05Z"
updated: "2026-08-29T15:39:05Z"
---

## Objective

Deliver the V1 workflow platform through thin, executable vertical slices: server-owned workflow state, a deterministic Go runtime, a generic Svelte Flow editor, and a safe embeddable surface.

## Delivery rules

- API first: the editor is a client of the REST API; it never owns authoritative workflow state or executes workflows.
- A draft graph may be incomplete. Activation and execution must compile and validate it.
- Workflow JSON contains credential references only; it never contains plaintext secrets.
- Persist execution and node-run evidence from the first runnable slice. Redact sensitive inbound and outbound data before persistence.
- Implement the standalone editor before the full embed session experience; preserve the embed boundary in all routes and API choices.
- Do not make n8n JSON or an AI framework the runtime contract.

## Roadmap order

Foundation → domain contract → workflow CRUD → registry → graph runtime → dashboard shell → first editor slice → execution inspector → credentials/expressions/HTTP → webhook/scheduler → SQL → AI → Go Code → embed → n8n interop.

## Product references

- `gflow-prd-v1.md` §§3–5, 17–25, 33–42, 49–60, 65.
- `design-refs/n8n/INDEX.md`: interaction and information-architecture reference only; do not copy n8n branding or visual style.

## Agent workflow

- Use the `pine` skill and update this epic/child ticket with decisions and verification evidence.
- Use `find-docs` before relying on library-specific APIs or configuration; record the exact official documentation consulted in the child ticket.
- Use implementation, testing, debugging, and verification skills only when their trigger applies; do not invoke generic Superpowers by default.
