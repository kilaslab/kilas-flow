---
id: FEAT-4fp51b
title: 'Per-tenant settings: a stored settings document and an update endpoint on the admin tenant API'
status: todo
priority: medium
labels:
    - saas
    - tenancy
    - api
parent: EPIC-7c3ry9
created: "2026-09-25T10:08:12Z"
updated: "2026-09-25T10:08:12Z"
---

# Description

A tenant can be created, read and deleted, but nothing about it can be changed afterwards. The operator admin API exposes only `GET`/`POST /tenants` and `GET`/`DELETE /tenants/{id}` (internal/api/handlers/admin.go:266-290), and `tenantModel` has no settings columns. Per-tenant behaviour that exists today is file config: node-pack visibility is `packs.visible_to` in the deployment config (internal/config/packs_visibility.go), not tenant data.

Several tickets in this epic assume a place to store and change per-tenant settings: FEAT-rdfjh1 (`limits`), FEAT-mngmn1 (a tenant API field for allowed/denied node types) and FEAT-39ttf6 (an operator-configured notification endpoint). Each would otherwise invent its own storage and endpoint.

Found by the 2026-09-25 audit of this epic's tickets against the code.

# Acceptance Criteria
- [ ] Tenants carry a typed, versioned settings document (a JSON column or a side table), with one migration for both dialects.
- [ ] `PATCH /api/v1/admin/tenants/{id}` (operator key only, like the rest of the admin surface) updates settings with merge semantics, and `GET /tenants/{id}` returns them.
- [ ] Unknown keys are refused with a named problem code, and each consumer registers and validates its own section, so FEAT-rdfjh1 and FEAT-mngmn1 add fields without touching the endpoint.
- [ ] Reads on hot paths (claim, compile, webhook ingress) are cached and invalidated on update, including across processes.
- [ ] The SDK and the admin client expose the update. The tenancy docs describe settings versus deployment config and which one wins.

# Implementation Plan

# Notes

Foundation for FEAT-rdfjh1 and FEAT-mngmn1 (recorded as their deps). Parallel branches collide on migration numbers, so land this before the tickets that add settings.

# Related Files

internal/api/handlers/admin.go:266-290 (tenant routes)
internal/config/packs_visibility.go (current per-tenant behaviour as file config)
docs/src/content/docs/concepts/tenancy-and-embedding.md:281-284 ("no per-tenant configuration")

# Attachments
