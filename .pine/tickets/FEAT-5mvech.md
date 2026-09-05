---
id: FEAT-5mvech
title: Write the architecture and concepts documentation
status: todo
priority: high
labels:
    - docs
deps:
    - FEAT-nxxbs5
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:54:02Z"
updated: "2026-09-05T11:54:02Z"
---

## Scope

The best explanatory writing in this repository is in Go package doc comments, and none of it is readable by anyone who has not cloned the repository and opened the right file. `internal/routing/doc.go` explains why routing metadata is data rather than code. `internal/nodepack/nodepack.go` opens by explaining why a pack is committed JSON and not generated Go and not an OpenAPI document parsed at boot. `internal/embed/embed.go` documents the token format and why the signature is verified before the payload is parsed. `internal/api/docs.go`, `internal/webhook/doc.go`, `internal/web/embed.go` and `internal/database/database.go` each carry the same quality of reasoning.

A reader deciding whether to build on KilasFlow needs that reasoning and cannot get it. The `README.md` "Layout" section names each package and its milestone in one line apiece; the PRD describes an intended design under a former project name; neither explains how the running system actually behaves.

The concepts that have to be written down, because a host integrator hits every one of them:

- **The execution model** — how a workflow becomes an IR, how the runner claims and runs it, `execution.max_concurrent`, the lease-based durable queue in `GORMExecutionStore.ClaimNext`, and the nine SSE event types that report it.
- **Items and lineage** — the item contract, binary references, and pairedItem provenance, which V2-p1-2 introduces and which 32% of real n8n expressions depend on.
- **Expressions** — the two dialects that must stay explicit: n8n marks an expression with a leading `=` on a plain string, KilasFlow uses `{"mode":"expression","value":…}`. Plus the allowlisted roots, and `$env` being deliberately narrowed to `KILASFLOW_WORKFLOW_ENV_*` so a workflow can never read the DSN or the master key.
- **The node registry** — `Definition`, the closed `PropertyKind` set, `displayOptions` visibility, downward version resolution (`Resolve` picks the highest registered version at or below the requested one, which is what lets an imported n8n `typeVersion` of 3.4 run against a v1 node), and the three sources `builtin` / `pack` / `sidecar` with their namespacing rules.
- **Credentials** — sealed payloads against public fields, `Split`, `AllowedDomains`, and the declarative authentication placement.
- **Tenancy and the embed boundary** — `TenantScope`, the `kfe1.` token, scopes and their implication rule, exact-origin matching, and `permits()` default-denying.
- **Webhooks** — the opaque per-tenant route segment, why an unknown, inactive and wrong-method request all return an identical 404, and the body and timeout bounds.
- **Safety boundaries** — `internal/safehttp`'s dial-time and per-redirect address checks, the internal-database guard, and the WASM Code node's zero-capability guest.

This is a documentation ticket. It writes down what the system does today; it does not change behaviour, and where a doc comment is stale it says so on this ticket rather than editing code.

## Acceptance criteria

- [ ] A reader who has never opened this repository can explain, after the Concepts section, how a workflow gets from a trigger to a node run to a recorded execution.
- [ ] The two expression dialects are documented explicitly, including which one appears in an imported n8n workflow and which one KilasFlow stores.
- [ ] Node versioning is documented with its downward-resolution rule and a worked example of an imported `typeVersion` that no registered version matches exactly.
- [ ] The three registry sources are documented with their namespacing rules, including that only built-in registration may claim the `kilasflow.` prefix.
- [ ] The embed boundary is documented as a security boundary: token format, scope implication, exact-origin matching, the fifteen-minute default and thirty-minute cap, and the fact that `permits()` denies anything it does not recognise.
- [ ] The safety boundaries are documented together in one place, so an operator can enumerate what a workflow can and cannot reach without reading Go.
- [ ] Every concept page links to the API reference operations and to the source package it describes, so the documentation is a route into the code rather than a replacement for it.
- [ ] Any doc comment found to be stale while writing is listed on this ticket with its file and the correction, for a follow-up rather than a silent edit.

## Implementation Plan

Write from the source, not from the PRD. `gflow-prd-v1.md` describes an intended system under a former name and has demonstrably drifted — its recommended Dockerfile names different base images than the one that ships. Treat it as historical context and the packages as the authority.

Order the pages by what a reader needs first, which is not the order the system is built in. Execution model, then items and expressions, then the node registry, then credentials, then tenancy and embedding, then safety. A reader arrives wanting to know what happens when a workflow runs; the registry is only interesting once they know why it matters.

Use diagrams sparingly and only where the prose genuinely fails — the execution lifecycle and the embed handshake are the two that earn one. Starlight renders Mermaid, so a diagram is text in the repository and diffable, which matters for something that must stay true as p1 through p9 change the engine underneath it.

The largest risk to this ticket is that it documents a moving target. p1 changes branch pruning, pairedItem lineage, trigger roots, loops, error handling and the expression engine; p2 changes the registry's property kinds and port descriptors. Two mitigations, and the ticket should adopt both. Write each page against what ships today and date it. And for a concept a p1 or p2 ticket is about to change, say what is true now and link the ticket that changes it, rather than either omitting it or describing an unbuilt future as present tense.

One thing to resist: do not write a page per Go package. The package layout is an implementation fact and a reader does not have it. Write a page per concept, and let a concept draw on several packages — the safety boundaries page pulls from `internal/safehttp`, `internal/sqlnode`, `internal/runcode` and `cmd/kilasflow/main.go`, and it is one idea.

## References

- Roadmap plan, p10 section, entry V2-p10-11: `.pine/roadmap.md`.
- `internal/routing/doc.go` — why routing metadata is data, and what the interpreter refuses.
- `internal/nodepack/nodepack.go` — the opening comment on why a pack is committed JSON.
- `internal/embed/embed.go` and `internal/embed/doc.go` — the `kfe1.` format, `Session`, `Scope`, `Allows`, lifetimes and origin matching.
- `internal/api/middleware/embed.go` — `permits()` and its default-deny arm.
- `internal/node/registry.go` — `Definition`, `Source`, `BuiltinPrefix`, `validateDefinition` and `Resolve`.
- `internal/property/property.go` — the closed `PropertyKind` set and `TypeOptions`.
- `internal/expression/doc.go` — the expression grammar and roots.
- `internal/engine/runner.go` — `Executor`, `Request`, and the run loop.
- `internal/repository/executions.go` — `ClaimNext` and the keyset cursor.
- `internal/webhook/webhook.go`, `internal/repository/webhooks.go` — `mintWebhookRoute` and the uniform 404.
- `internal/safehttp/safehttp.go`, `internal/sqlnode/sqlnode.go`, `internal/runcode/` — the safety boundaries.
- `cmd/kilasflow/main.go` — `workflowEnvironment`, `databaseGuard`, `outboundPolicy`, and the composition order the registry depends on.
