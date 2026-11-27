---
id: FEAT-mngmn1
title: Allow or deny built-in node types per deployment or tenant
status: done
priority: medium
labels:
    - saas
    - tenancy
    - node-catalog
deps:
    - FEAT-4fp51b
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-11-27T08:51:26Z"
---

# Description

`packs.visible_to` cannot scope built-in node types (`docs/src/content/docs/guides/tenant-scoped-nodes.md:137-139`). A SaaS may need to hide nodes its customers should not use: SQLite (file paths on the server), Code, Postgres/MySQL, or raw HTTP.

# Acceptance Criteria
- [x] `nodes.deny` and `nodes.allow` config entries, written `type` or `type=tenant`, plus a tenant API field.
- [x] Hidden types behave like `node.not_available` in the catalogue, at compile, at activation and at claim.
- [x] Engine-required types (error trigger, sub-workflow trigger) cannot be denied.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid. tenant-scoped-nodes.md:137-138 still says built-ins cannot be scoped; no `nodes.deny`/`nodes.allow` anywhere. Reuse `node.not_available` from internal/workflow/compiler.go:153 (RestrictedCatalog).
- Coordinate with FEAT-rdfjh1: both add a field to the tenant API, which has no update endpoint today (FEAT-4fp51b, recorded as a dep).

## Progress

Branch worktree-agent-a20d27b55d3377478 (FEAT-mngmn1).

- **Mechanism**: built-in allow/deny state lives on `node.Registry` (`internal/node/availability.go`) and is consulted by `VisibleTo`, `ListFor`, `ResolveFor` and the `TenantView` the compiler gets from `workflow.CatalogFor`. A hidden built-in is therefore `node.not_available` at compile, activation, manual run and the worker's claim, and absent from the catalogue and the icon/option routes, through the same path pack visibility uses. No new gate, no call site changed. `Scoped()` is true for hideable built-ins so the icon route stays out of shared caches.
- **Semantics** (documented in tenant-scoped-nodes.md): the allow list that applies to a tenant is the union of deployment-wide entries, entries naming the tenant and the tenant's own `nodes.allow` setting; non-empty means only listed built-ins (plus engine-required) are visible. Deny lists union the same way. Deny wins. Only built-ins are affected (packs keep `packs.visible_to`). A built-in's tool form follows its base (a rule naming a tool form is refused). A tenant setting adds to the deployment's lists and can never lift a `nodes.deny` entry.
- **Config**: `nodes.allow` / `nodes.deny`, entries `type` or `type=tenant` (`internal/config/nodes_availability.go`), grammar errors name `nodes.deny[N]`. An engine-required type on the deny list refuses the boot. `ApplyAvailability` (after the last registration, like `ApplyVisibility`) refuses an unregistered type, a pack type, a tool form and a malformed tenant ID. The engine-required list lives in `node.EngineRequiredTypes()`; a test pins it to `nodes.ErrorTriggerNodeType` / `nodes.ExecuteWorkflowTriggerType`.
- **Tenant API**: new `nodes` section (`allow`, `deny`) of the FEAT-4fp51b settings document, registered by `Registry.RegisterSettings`. No change to the PATCH endpoint. An engine-required type on `deny`, an unregistered or non-built-in type and a tool form are `tenant_settings_invalid` (422) located at `body.settings.nodes.deny[N]`. To keep this from failing open, `tenantsettings.RegisterChecked` adds a write-only check: a stored section that failed validation on read would be skipped whole (all its deny entries lost), so the registry checks happen on PATCH only and read time is lenient (unknown type hides nothing, engine-required is never hidden).
- **Failure mode**: if a tenant's lists cannot be read (settings store error and nothing cached), the tenant loses every hideable built-in (engine-required types and packs stay) and an error is logged. Chosen over failing open so a settings outage cannot switch a deny list off; the cache already serves last-known settings on a store error, so this only bites a cold cache. Other processes see a PATCH within the 5 s cache window.
- **Tests**: `internal/node/availability_test.go` (deny, tenant-scoped deny, allow, union, deny wins, engine-required, tool forms, settings source, fail-closed, Compile code), `internal/config/nodes_availability_test.go`, `internal/tenantsettings` write-check test, `internal/api/node_availability_test.go` (PATCH, catalogue, activation, 422), `internal/engine/builtin_availability_test.go` (activation, manual run, claim, deny set after queueing).
- **Generated**: `make generate-config-reference`, generate-types (sdk), generate-api (web), generate-api-reference, generate-docs-facts all run and committed; the only generated diff outside the config reference is the update-tenant description now reading "Sections this server accepts: nodes, rateLimit".
- **Not done / follow-ups**: no web admin UI for the section (there is none for `rateLimit` either). `make docs-build` passes (internal links valid).

# Related Files

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-11-27.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (13):
  - `3be8b6fb` — merge: allow or deny built-in node types per deployment or tenant (FEAT-mngmn1)
  - `a0222836` — chore(pine): FEAT-mngmn1 records the docs build result
  - `9f06af6c` — chore(pine): FEAT-mngmn1 progress, acceptance criteria ticked
  - `77eda7b1` — FEAT-mngmn1: name the availability calls in the shipped-catalogue drift allowlist
  - `f9ae5851` — FEAT-mngmn1: document allowing and denying built-in node types
  - `e85ec8ca` — FEAT-mngmn1: regenerate the API clients and reference for the nodes settings section
  - `434a3dbb` — FEAT-mngmn1: regenerate the configuration reference and config.example.yaml
  - `7ecdcb34` — FEAT-mngmn1: wire built-in availability into boot and prove it through the API and engine
  - `1ac8b4ea` — FEAT-mngmn1: add a write-only check to tenant settings sections
  - `1c647dce` — FEAT-mngmn1: parse nodes.allow and nodes.deny
  - `ee9cdc89` — FEAT-mngmn1: hide built-in node types through the registry's visibility path
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
 1646 files changed, 275773 insertions(+), 7906 deletions(-)
```
