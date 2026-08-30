---
id: FEAT-kk9h5y
title: Define canonical workflow contract and durable persistence model
status: done
priority: critical
labels:
    - domain
    - contract
    - security
deps:
    - FEAT-209rxk
parent: EPIC-c7gbdp
phase: p1
created: "2026-08-29T15:39:39Z"
updated: "2026-08-30T07:06:41Z"
---

## Scope

Define the server-owned workflow contract and persistence boundary before any editor or runtime feature commits to a shape. Include workflow JSON, runtime IR, graph/port semantics, lifecycle/version semantics, tenancy placeholders, and durable execution-record schemas.

## Acceptance criteria

- A versioned canonical Workflow JSON schema represents nodes, positions, parameters, credential references, connections, and settings; imported formats are explicitly out of scope for this schema.
- A compiler boundary translates canonical JSON to internal IR; JSON is never executed directly.
- The contract distinguishes draft/save from activate/run: incomplete drafts can persist, while activation/execution returns deterministic, structured validation errors for invalid topology, unknown node types, incompatible ports, or absent required configuration.
- Internal persistence has migrations/models for workflows, workflow versions, executions, and execution node runs, including tenant ownership fields or a documented extension point.
- Node inputs/outputs use item semantics and support multiple outputs, including true/false branch outputs; public API DTOs are independent from ORM models.
- Unit tests cover schema/compiler validation and a migration test verifies a fresh SQLite database.

## References

- PRD: §§17–22, 35, 54–57.
- UI/workflow reference: `design-refs/n8n/14-canvas-if-branching.png`; `design-refs/n8n/INDEX.md` entries 02 and 14.

## Relevant documentation

- Consult current GORM migration guidance through `find-docs` if changing ORM APIs. Standard-library JSON/schema choices need no external dependency unless introduced by this ticket.

## Relevant skills

- `pine` — write architectural decisions and verification into the ticket.
- `find-docs` — mandatory if adding/changing GORM or a schema-validation library.
- `test-driven-development`, `systematic-debugging`, `verification-before-completion` — use when applicable.

## Implementation notes

- Canonical Workflow JSON v1 lives in `internal/workflow` plus `schemas/workflow-v1.schema.json`. `DecodeDocument` rejects unknown fields and schema-required structural omissions, while `ValidateDraft` deliberately permits incomplete topology and node configuration.
- `workflow.Compile` is the only document-to-IR boundary. It returns stable structured errors, resolves named output ports to output indexes, copies dynamic values defensively, and never exposes canonical document maps to the runtime IR.
- Every save appends an immutable `workflow_versions` snapshot. `Activate` compiles only the latest revision and never changes the active snapshot when a newer draft is saved. Save and activate lock the workflow identity before deriving or pinning a revision, preventing stale writes under concurrent PostgreSQL transactions. All stores require an explicit `TenantScope`; standalone callers use `DefaultTenantID`.
- Startup registers the private GORM models for workflows, workflow versions, executions, and node runs. Execution records pin their workflow version and payloads are accepted only as valid, redaction-ready JSON.

## Documentation consulted

- GORM AutoMigrate: https://gorm.io/docs/migration.html (Context7, 2026-08-30). The existing explicit `database.Migrate(db, models...)` boundary remains the P1 migration strategy.
- GORM row locking: https://gorm.io/docs/advanced_query.html (Context7, 2026-08-30). `clause.Locking{Strength: "UPDATE"}` serializes revision derivation and activation on the workflow row.

## Verification

- `make test` — passed with `-race`.
- `make lint` — passed (`go vet`, gofmt check, Svelte check with 0 errors/warnings).
- `make smoke-sqlite` — passed against a fresh database and rebuilt embedded binary.
- `KILASFLOW_SMOKE_SKIP_BUILD=1 make smoke-postgres` — passed; the PostgreSQL migration test now includes the real P1 models.

## Work Evidence

Closed by `pine close --evidence` on 2026-08-30.

- Base: _(none — ticket predates git history or creation time unknown; showing uncommitted changes only)_
- _(no file changes detected since ticket creation)_
