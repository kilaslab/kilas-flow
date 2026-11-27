---
id: FEAT-r267jj
title: Host-declared confinement for embed sessions
status: doing
priority: high
labels:
    - saas
    - embedding
    - security
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

A workflow session's confinement (the credentials, data tables and sub-workflows its document may reference) is derived at mint from the revision the owner authored (`internal/api/handlers/embed.go:95-131`, `internal/api/handlers/embedscope.go:55-74`). So an end user building a new automation inside the embed cannot select a credential or data table that the host provisioned for their org, unless the host rewrites the workflow document first. A blank workflow can reference nothing. None of this appears in the guides.

# Acceptance Criteria
- [x] `POST /api/v1/embed-sessions` accepts `allow: { credentials?: string[] | "tenant", datastores?: string[] | "tenant", workflows?: string[] | "tenant" }`. Every id is validated against the minting tenant, and a foreign or unknown id returns 404.
- [x] `"tenant"` is accepted only from an unbound key.
- [x] The token carries the declared set. Save, publish, restore and run accept the union of the derived and declared sets.
- [x] The embedded credential picker lists exactly the allowed credentials.
- [x] The embedding guide documents confinement, the 403 message and these options.
- [x] Tests cover a cross-tenant id, an unlisted id and `"tenant"`.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid. Refs current: `Create` embed.go:90, derivation embed.go:103-113, `embedConfinementOf` embedscope.go:56-73; `embedSessionBody` (embed.go:59-66) has no `allow`, `Confinement` (internal/embed/confinement.go:34-43) holds only the derived set.
- The credential list is already filtered to the confinement (`embedAllowsCredential`, internal/api/handlers/credentials.go:222-244): the picker AC means widening that filter to the declared set, not a new picker.
- Enforcement sites to extend: workflows.go:603/:622 (publish/restore, `embedVersionProblem`), :678 (save, `embedDocumentProblem`), :874 (run).

## Progress

2026-11-27 (worktree branch, not pushed). Landed:
- `embed.Confinement` gains `TenantCredentials`/`TenantDatastores`/`TenantWorkflows` flags (token keys `tcr`/`tds`/`twf`, omitted when false) and `Union`. A flag drops its kind's id list from the token. Flags only widen, so an older server that ignores them reads a token as narrower; tokens without them decode unchanged (no `kfe1` version bump).
- `POST /embed-sessions` takes `allow` (`EmbedAllowList`: `string[] | "tenant"`, oneOf in OpenAPI, max 50 ids per kind). Each id is looked up in the minting tenant (credential store, datastore engine, workflow store) and an unknown or foreign id answers 404. A declared data table is granted by id and its current name. `allow` on a datastore session is 422. With no store wired, declared ids are 503, not trusted.
- "Unbound key": the API has no such type. Implemented as tenant-wide authority (`mintsWithTenantAuthority`): no embed session, not a scoped key, no `WorkflowID` binding. A dashboard user and auth-off are accepted. The route middleware already refuses scoped keys and embed sessions on this endpoint, so this check is defence in depth.
- The token carries derived ∪ declared, so every enforcement site (save, publish, restore, activate, run, retry, load-options credential check) accepts the union unchanged. The credential list is unrestricted (within the tenant) under `credentials: "tenant"`.
- Tests: internal/embed/confinement_test.go, nodes/embedscope_test.go, internal/api/handlers/embed_allow_test.go, internal/api/embed_allow_test.go (two tenants over one DB: cross-tenant and unknown ids 404, unlisted id 403, "tenant" flag, picker union, save/publish/restore/run of a declared credential, data table by id and name, declared workflow).
- Docs: guides/embedding.md section "What the editor may reference: confinement and allow" (403 detail lines included), concepts/tenancy-and-embedding.md, CHANGELOG, sdk/CHANGELOG. SDK `createEmbedSession` types `allow`. Regenerated: web API client, SDK types, API reference, SDK reference; docs-facts up to date.

Remains (not in the AC):
- The embedded editor's data table and workflow pickers still list nothing / only the own workflow (loadoptions/datastores.go, loadoptions/workflows.go). A declared table or workflow saves when chosen By ID or By Name, but cannot be picked from a list yet.
- A declared grant can outlive its session: once a revision using it is published (or saved on a never-activated workflow) the next session derives it. Documented in the guide and in `embedConfinementOf`; a reviewer should confirm that is acceptable.

# Related Files

# Attachments
