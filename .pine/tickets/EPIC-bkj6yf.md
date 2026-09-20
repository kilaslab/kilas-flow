---
id: EPIC-bkj6yf
title: 'Embedded SaaS readiness: tenant lifecycle, per-tenant nodes and an agent surface'
status: todo
priority: high
labels:
    - platform
    - api
    - security
created: "2026-09-20T05:26:31Z"
updated: "2026-09-20T05:26:31Z"
---

## Why this epic exists

KilasFlow is being positioned as an embedded workflow engine for SaaS products. An audit of
the source (2026-09-20) found that the embedding boundary itself is sound — tenant is an
argument to every repository call, the embed token is a narrowing credential with a
default-deny path gate, and the editor mounts in a host iframe — but that several things a
SaaS host is promised are either unreachable, incomplete, or absent.

This epic collects the work that closes the distance between "embed the editor and the API"
(working today) and "run this as the workflow layer of somebody else's product" (not yet).

## What is in scope

- Board and documentation honesty: three tickets are closed `done` with every acceptance
  criterion unticked, and the docs were written against them.
- Tenant lifecycle: a customer cannot be deleted, so a data-deletion promise cannot be met.
- Idempotency: a retried run is a second run.
- Datastore schema versioning: the migration runner exists and is never started.
- Inbound webhook hardening: default no authentication, plus a legacy cross-tenant route
  fallback.
- Per-tenant node visibility: custom nodes are deployment-global.
- An agent surface (CLI first, MCP adapter later) so an AI agent can create, edit, trigger
  and debug workflows.

## What is deliberately out of scope

- A public Go library facade (`pkg/engine`). Every engine seam lives under `internal/`; that
  is a deliberate ceiling until a host demands in-process embedding.
- `pkg/datastore` storage substitution. The supported posture is a shared database plus
  `database.table_prefix`.
- Row-level security or schema-per-tenant.
- WASM pack host ABI and sidecar wiring: those are decisions to be taken explicitly (see the
  board reconciliation ticket), not assumed.
