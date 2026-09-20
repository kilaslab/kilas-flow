---
id: FEAT-emf6k5
title: Per-tenant node visibility so a host can ship its own nodes to one customer
status: doing
priority: medium
labels:
    - registry
    - packs
    - platform
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:52Z"
updated: "2026-09-20T07:42:30Z"
---

## Problem

A SaaS host that ships its own nodes (its CRM nodes, its WhatsApp nodes) publishes them to
every tenant of the deployment. There is no per-tenant dimension in the node catalogue, so
"custom node milik MyCRM" is not expressible.

## Evidence

- The registry key is `{nodeType, version}` with no tenant dimension
  (`internal/node/registry.go:238`); `RegisterFrom` takes a source but no scope.
- Directory packs are installed at composition, before the registry is shared:
  `internal/nodepack/loaddir.go` (`LoadDir`) and `DirDeps` carry no tenant.
- The catalogue endpoint returns everything: `internal/api/handlers/nodes.go:127` (`List`).
- The only availability concept is deployment-wide: `api.Deps.NodeAvailability` returns a map
  of node type → reason (`internal/api/server.go:108-111`), stamped per deployment.
- Embed sessions can read the whole catalogue (`/node-types` is allowed on `workflow:read` in
  `internal/api/middleware/embed.go`), so one host's packs are visible to every tenant's
  editor.

## Acceptance criteria

- [ ] A pack can declare the tenants it is visible to (or an operator can scope an installed
      pack to a set of tenants), and a node outside that set is invisible in `GET /node-types`
      for that tenant.
- [ ] The compiler refuses a document that references a node type invisible to the tenant
      being compiled, with a diagnostic that distinguishes "unknown type" from "not available
      to you".
- [ ] Execution refuses a run whose document references an invisible type, so a workflow
      copied between tenants cannot smuggle one in.
- [ ] An unscoped pack keeps today's behaviour (visible everywhere), so nothing existing
      changes.
- [ ] Tests cover: two tenants, one scoped pack, catalogue and run behaviour for each.

## Out of scope

Runtime install/hot-reload of packs, and per-tenant pack upload over HTTP. Both are separate
decisions.
