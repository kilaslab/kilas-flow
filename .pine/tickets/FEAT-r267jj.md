---
id: FEAT-r267jj
title: Host-declared confinement for embed sessions
status: done
priority: high
labels:
    - saas
    - embedding
    - security
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-11-27T08:51:26Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-11-27.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (10):
  - `0c336a97` — merge: host-declared confinement for embed sessions (FEAT-r267jj)
  - `f8afee01` — chore(pine): FEAT-r267jj progress, acceptance boxes ticked
  - `88954e3c` — FEAT-r267jj: say that a declared grant can outlive its session
  - `71ab72bc` — FEAT-r267jj: document embed confinement, the allow set and its 403
  - `07c9c8aa` — FEAT-r267jj: type the allow set on the SDK's createEmbedSession
  - `8fb285c2` — FEAT-r267jj: regenerate the API client, SDK types and API reference for allow
  - `ed61e418` — FEAT-r267jj: accept a host-declared allow set on embed session mint
  - `a1d5d33d` — FEAT-r267jj: let a confinement open a kind to the tenant and take a union
  - `bf841006` — chore(pine): EPIC-7c3ry9 records its dependencies and audit notes, and files four infra tickets
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .env.example                      |    8 +-
 .github/assets/editor.png         |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml          |  108 +++-
 .github/workflows/code-corpus.yml |   54 ++
 .gitignore                        |    4 +
 .pine/MEMORY.md                   |    2 +
 .pine/memory/code-node.md         |   30 +-
 .pine/memory/docs.md              |    5 +-
 .pine/memory/e2e.md               |    3 +
 .pine/memory/licensing.md         |   15 +-
 .pine/memory/n8n-reference.md     |   10 +-
 .pine/memory/persistence.md       |    6 +-
 .pine/memory/testing.md           |    4 +-
 .pine/memory/web-editor.md        |    7 +-
 .pine/roadmap.md                  |    8 +-
 .pine/tickets/BUG-0592hz.md       |   62 ++
 .pine/tickets/BUG-0bzsa1.md       |  464 +++++++++++++++
 .pine/tickets/BUG-0grt9g.md       |  427 +++++++++++++
 .pine/tickets/BUG-0xv7bg.md       |   68 +++
 .pine/tickets/BUG-14gp8r.md       |  198 +++++++
 .pine/tickets/BUG-15st2k.md       |  198 +++++++
 .pine/tickets/BUG-1vh6k6.md       |   51 ++
 .pine/tickets/BUG-2d1pdx.md       |   49 ++
 .pine/tickets/BUG-2eryxn.md       |   95 +++
 .pine/tickets/BUG-2mes2k.md       |   83 +++
 .pine/tickets/BUG-2n4rfz.md       |   58 ++
 .pine/tickets/BUG-2vcwjf.md       |  126 ++++
 .pine/tickets/BUG-2xrz6c.md       |   51 ++
 .pine/tickets/BUG-2z8geh.md       |  136 +++++
 .pine/tickets/BUG-3k12ky.md       |  187 ++++++
 .pine/tickets/BUG-3mem9s.md       |  105 ++++
 .pine/tickets/BUG-3qxx0j.md       |   70 +++
 .pine/tickets/BUG-46g75c.md       |  219 +++++++
 .pine/tickets/BUG-49vf3j.md       |  428 ++++++++++++++
 .pine/tickets/BUG-4ch186.md       |   85 +++
 .pine/tickets/BUG-548bk9.md       |  179 ++++++
 .pine/tickets/BUG-56qqgx.md       |   88 +++
 .pine/tickets/BUG-5bgx5c.md       |   81 +++
 .pine/tickets/BUG-5dn8hr.md       |   39 ++
 .pine/tickets/BUG-5fhcx7.md       |   50 ++
 .pine/tickets/BUG-5xkexq.md       |   63 ++
 .pine/tickets/BUG-605n21.md       |  154 +++++
 .pine/tickets/BUG-66fhea.md       |  112 ++++
 .pine/tickets/BUG-6d6wbg.md       |  115 ++++
 .pine/tickets/BUG-6gkd12.md       | 1183 +++++++++++++++++++++++++++++++++++++
 .pine/tickets/BUG-719gaz.md       |  117 ++++
 .pine/tickets/BUG-745mh2.md       |   60 ++
 .pine/tickets/BUG-8xz9t1.md       |  511 ++++++++++++++++
 .pine/tickets/BUG-9296bf.md       |  532 +++++++++++++++++
 .pine/tickets/BUG-9dw5me.md       |   66 +++
 ...
 1646 files changed, 275700 insertions(+), 7906 deletions(-)
```
