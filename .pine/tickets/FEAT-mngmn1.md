---
id: FEAT-mngmn1
title: Allow or deny built-in node types per deployment or tenant
status: doing
priority: medium
labels:
    - saas
    - tenancy
    - node-catalog
deps:
    - FEAT-4fp51b
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T10:08:26Z"
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
