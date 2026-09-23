---
id: EPIC-62zt4j
title: 'Documentation website for SaaS integrators: embed, API, custom nodes, operate'
status: todo
priority: high
labels:
    - docs
    - saas
    - embedding
created: "2026-09-23T02:03:24Z"
updated: "2026-09-23T02:03:24Z"
---

# Description

KilasFlow's main audience is SaaS products that **embed** it: they mount the editor in their own UI, map their organisations to tenants, drive the API from their backend, read and write Data tables from their app, and add nodes for their own product's API. The documentation site (Astro Starlight in `docs/`) is careful, candid and CI-gated, with generated API and config references and link validation. But it is written for contributors and operators, it is not published anywhere, and an integrator arriving cold cannot complete the core journey from the docs alone.

The 2026-09-23 docs audit walked eight integrator golden paths against the live product:

| Path | Result |
|---|---|
| (a) Embed the editor in 15 minutes | **Blocked**: every Save and Run returns 403 from a cross-origin host (BUG-b3p8va). Session confinement, token refresh and operator-key provisioning are also undocumented |
| (b) Map organisations to tenants, auth, SSO | Partly: the provisioning recipe exists only in an example README; scoped keys are undocumented; nothing says there is no SSO |
| (c) Call the API from a backend | Partly: no request/response field docs, two pagination conventions, no outbound events, and the SDK is on neither npm nor the site |
| (d) Data tables from the host app | Mostly: four sources disagree on the datastore embed surface, and the reference marks allowed operations "Deny" |
| (e) Nodes for my product's API | Only tenant-scoped nodes are complete: OpenAPI generation is undocumented, the scaffold's credential type can't exist, the trigger signing format is undocumented, and the WASM guide contradicts the reference |
| (f) White-label | Partly: branding is documented; locales, theming limits and go-live aren't |
| (g) Deploy for SaaS | Mostly, but scattered: no shared-Postgres (`table_prefix`) recipe or sizing guidance |
| (h) Reference | Mixed: API reference is operation-level only; no per-node or credential-type reference |

The detailed defects (DOC-2 … DOC-28) are attached to the child tickets. DOC-1, the cross-origin embed bug, is a product bug filed as BUG-b3p8va under EPIC-8rbys7, and the embed quickstart depends on it.

# Goals

- A public docs site at a stable URL, organised **integrators first**, with three tracks on the landing page: Embed in your SaaS, Integrate via API, Build nodes for your API. Operate and Reference sit beside them.
- Every integrator golden path (a)–(h) can be completed from the docs alone, and the embed quickstart is **tested in CI** against a cross-origin host.
- References that cannot drift: API (fields, examples, curl/TS/Go), embed permit table, node catalogue and credential types are generated from the binary and gated in CI, like the existing API and config references.
- Agent-readable: `llms.txt` / `llms-full.txt`, linked with the embedded skills bundle.

# Proposed information architecture

Status in brackets: exists / rewrite / new.

- **Get started:**
  - Home with three tracks (rewrite)
  - What KilasFlow is (exists)
  - How it fits your stack (new)
  - Run locally, including the operator and embed keys (rewrite)
  - Quickstart: embed in 15 minutes (new)
  - First workflow (rewrite the stub)
- **Embed in your SaaS:**
  - Who authorises what (rewrite)
  - Map organisations to tenants (rewrite)
  - Provision tenants with the operator key (new)
  - Identity: auth modes, API keys and scopes, SSO stance (new)
  - Embed sessions: scopes, confinement, lifetime and refresh, origins and CORS (new)
  - Mount the editor (new)
  - Watch runs live (new)
  - Go live from your UI (new)
  - White-label and localisation (rewrite)
  - Data tables for your app (rewrite)
  - Troubleshooting embeds (new)
  - Reference host walkthrough (new)
- **Integrate via API:**
  - API overview and stability (rewrite)
  - TypeScript SDK (new)
  - Go and curl (new)
  - Pagination (new)
  - Errors (exists)
  - Idempotency (exists)
  - Trigger workflows from your app (rewrite)
  - Get notified of results (new)
  - Import and export n8n (rewrite)
- **Build nodes for your API:**
  - Choosing a path: declarative pack, OpenAPI-generated pack, tenant-scoped node, WASM module, JS sidecar (new)
  - Declarative pack tutorial (rewrite)
  - Generate from OpenAPI (new)
  - Credentials for your API (new)
  - App events as triggers, with signing (new)
  - Tenant-scoped nodes (exists)
  - WASM module pack tutorial (new)
  - JS sidecar (move)
  - Install and distribute packs (rewrite)
- **Workflows:** execution model, items, expressions, triggers, credentials, concurrency, n8n migration (all exist)
- **Operate:**
  - Topologies (exists)
  - Deploy for SaaS (new)
  - Security and safety (exists)
  - Backups, upgrades and pinning (rewrite)
  - Tenant deletion (exists)
  - Configuration (exists)
  - Benchmark (exists)
- **Reference:**
  - HTTP API with fields, examples and tabs (rewrite the generator)
  - OpenAPI download (new)
  - Events, Errors, Contract (exist)
  - SDK TypeDoc (new)
  - CLI (exists)
  - Configuration (exists)
  - Expression grammar (exists)
  - Node catalogue, generated (new)
  - Credential types, generated (new)
  - Node pack format (rewrite)
  - Glossary (new)
- **About:** contributing, acceptance capstone, changelog

# Notes

- Product gaps found alongside the docs audit are tracked as product tickets in EPIC-7c3ry9 (for example FEAT-0xsc1s token refresh, FEAT-39ttf6 outbound events, FEAT-n12211 pack credential types, FEAT-hxztwz trigger signing): token refresh for a mounted editor, outbound execution events, generic credential types for a host API, and the app-fired trigger signing contract. The docs tickets here document them once they exist, or state their absence honestly until then.
- The `skills/kilasflow-embedding` bundle is currently the most accurate description of embedding. Use it as source material.
