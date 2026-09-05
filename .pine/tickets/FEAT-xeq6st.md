---
id: FEAT-xeq6st
title: Expose the Datastore management API
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-nrfg6e
    - FEAT-ddzk2k
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

`registerRoutes` in `internal/api/routes.go` mounts eight handler groups on the `v1` group at lines 22 to 29 — system, node types, workflows, executions, credentials, schedules, embed sessions and interop. There is no datastore group, `Deps` in `internal/api/server.go:39-63` carries no datastore collaborator, and a search for `datastore` across `internal`, `cmd`, `nodes`, `pkg`, `sdk` and `web/src` returns nothing but one vendored `node_modules` JSON file. The management surface the row store from V2-p9-2 exists to serve is absent entirely.

The shape to copy is settled and small. `Schedules.Register` at `internal/api/handlers/schedules.go:74-92` is the complete pattern — four `huma.Register` calls carrying `OperationID`, `Method`, `Path`, `Summary`, `Description` and a `Tags` slice, with `DefaultStatus: http.StatusCreated` on create and `http.StatusNoContent` on delete, a `Location` header on the created output, and a `problem` helper mapping `repository.ErrNotFound` to a 404 and everything else to a 422. `Executions.List` at `internal/api/handlers/executions.go:265-300` supplies what a row listing additionally needs: a `Cursor` query parameter, an `{items, nextCursor}` body, and the translation of `repository.ErrInvalidCursor` into a 400 rather than a 500. Tag strings become directories in the generated client — `"Workflow lifecycle"` produced `web/src/lib/api/generated/workflow-lifecycle` — so three resource groups mean three tags.

Row delete is the one operation where that shape must not be copied. Every other delete on this API takes a path identifier and removes exactly one record. A datastore row delete takes a filter, and a filter that is absent, empty or unrecognised has to be refused rather than read as "match everything", because the distance between those two readings is a customer's whole table.

The dependency on V2-p8-1 (`FEAT-ddzk2k`) is load-bearing rather than tidy. `cmd/kilasflow/main.go:171-188` builds its `api.Deps{…}` literal without ever assigning `Tenants`, and every handler constructor substitutes `defaultTenantResolver{}` when that field is nil — a resolver returning `repository.TenantScope{ID: repository.DefaultTenantID}`, the constant `"default"` at `internal/repository/workflows.go:17`.

That is why this ticket waits. A datastore API is host-driven by decision, so shipping one over a single shared `default` tenant pools every host customer's business rows into one bucket, and the cross-tenant isolation test V2-p9-12 owes the epic has no second tenant to write against through the public surface.

## Acceptance criteria

- [ ] A row delete carrying no filter, an empty filter list or an unrecognised operator is refused with a 422 and removes nothing, proven by a handler test asserting an unchanged row count.
- [ ] Every datastore operation resolves its tenant through the injected `TenantResolver`, and a request naming another tenant's datastore receives a 404, proven by a two-tenant handler test.
- [ ] `cmd/kilasflow/main.go` assigns `Tenants` in its `api.Deps` literal, and a test fails if any registered handler group falls back to `defaultTenantResolver`.
- [ ] The three resource groups appear in the generated OpenAPI document under their own tags, and `pnpm generate:api:check` exits zero against the committed client.
- [ ] A row-listing cursor the client did not receive from this API is answered with a 400 rather than a 500, matching `Executions.List`, proven by a handler test.
- [ ] An embed-session token is refused on every datastore path by the existing default-deny arm of `permits`, proven by a middleware test, until V2-p9-15 grants a scope.
- [ ] Creating a datastore returns 201 with a `Location` header under `/api/v1/datastores/`, matching the created-resource shape schedules and credentials already return.
- [ ] Every operation is exercised through a server built from `api.Deps` in the `api_test` package, run by hand with `make test` and recorded on this ticket.

## Implementation Plan

Do not begin with handlers. Confirm first that V2-p8-1 has landed and `Tenants` is populated in `cmd/kilasflow/main.go`, because tests written against `defaultTenantResolver{}` all pass under one tenant and prove nothing about isolation — they are exactly the tests V2-p9-12 would then have to throw away. A one-line assertion that the resolver in play is not the default one is the cheapest guard against that.

Put the three groups in one file, `internal/api/handlers/datastores.go`, with one `Datastores` struct and one `Register` mounting all three path families. **One handler or three.** Reject three structs sharing a store: rows are addressed under `/datastores/{id}/rows`, so a rows handler cannot be constructed without the datastore identity anyway, and splitting yields three constructors in `routes.go` to keep in step for no gain. Tag them `"Datastores"`, `"Datastore columns"` and `"Datastore rows"` so the generated client splits cleanly.

Model the filter as a typed body, not a query blob. Row listing takes `GET /datastores/{id}/rows` with `limit` and `cursor` as in `listExecutionsInput`, plus repeated flat filter triples; row delete takes `DELETE /datastores/{id}/rows` with a required body carrying the `{type, filters}` shape. Reject a single URL-encoded JSON query parameter: Huma cannot validate its interior, and the OpenAPI document degrades it to a bare string, stripping the generated client of every type the schema was written to provide.

Answer 503 when the store is nil, exactly as `Schedules` and `Executions` do, and leave `internal/api/middleware/embed.go` alone: its `permits` switch ends in a default arm at lines 147 to 150 that denies anything it does not recognise, so datastore paths are already closed to embed sessions. Pin that behaviour with a test and resist the urge to "fix" the 403 — granting the scope is V2-p9-15's work and it carries the vocabulary change with it.

The trap is the empty filter. Go decodes both an absent `filters` key and an explicit `[]` to a nil slice, so a guard phrased as "a body was supplied" accepts `{}` and `{"type":"and","filters":[]}` alike, compiles them to no predicate at all, deletes every row and returns 200 with a count that reads as success. Guard on the compiled predicate — refuse when the fragment list is empty after the operator switch has run — and test both encodings, because they arrive at the same nil and only one looks wrong in a request log.

Recommend the typed `DELETE` body over a `POST /datastores/{id}/rows/delete` action endpoint, which would be the only verb-in-path operation on an otherwise uniform surface. What reopens it is evidence from `make smoke-postgres` or a deployment that an intermediary strips `DELETE` bodies; the action endpoint is then the fallback and the filter shape carries over unchanged.

## References

- Roadmap plan, p9 section, entry V2-p9-7: `.pine/roadmap.md`.
- `internal/api/routes.go` — `registerRoutes` and the eight handler groups at lines 22 to 29 a datastore group joins.
- `internal/api/server.go` — `Deps` at lines 39 to 63, which needs the datastore collaborator and already declares `Tenants`.
- `internal/api/handlers/schedules.go` — `Register` at lines 74 to 92, the smallest complete example of the registration, created-output and `problem` shapes.
- `internal/api/handlers/executions.go` — `List` at lines 265 to 300, the cursor input, the `{items, nextCursor}` body and the `ErrInvalidCursor` to 400 translation.
- `internal/api/handlers/workflows.go` — `TenantResolver` and `defaultTenantResolver` at lines 17 to 27.
- `cmd/kilasflow/main.go` — the `api.Deps` literal at lines 171 to 188, which never assigns `Tenants`.
- `internal/repository/workflows.go` — `const DefaultTenantID = "default"` at line 17.
- `internal/api/middleware/embed.go` — the default-deny arm of `permits` at lines 147 to 150, which already closes datastore paths to embed sessions.
- `web/orval.config.ts` and `web/scripts/check-api-client.mjs` — tags-split generation and the staleness check `pnpm generate:api:check` runs.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 18-19 — the Data tables tab beside Workflows and Credentials, and a create dialog that takes a name only, so columns are always a second call. Captured from a live local n8n 2.x instance; gitignored, never vendored.
