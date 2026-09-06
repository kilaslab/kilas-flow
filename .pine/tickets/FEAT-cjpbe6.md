---
id: FEAT-cjpbe6
title: Isolate Datastore data from redaction, traces and other tenants
status: doing
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-nrfg6e
    - FEAT-1br8at
    - FEAT-a94c8y
    - FEAT-nrfz6m
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-06T04:48:34Z"
---

## Scope

`payload` in `internal/repository/executions.go:605-611` is the funnel every durable execution and node-run write passes through, and it ends in `execution.Redact(encoded)`; `internal/events/events.go:139` applies the same `Redact` to every live event's `Data`. `Redact` decides by key, never by content: `sensitiveKeys` at `internal/execution/redact.go:16-54` holds `"token"`, `"secret"`, `"apikey"`, `"password"`, `"credential"`, `"signature"`, `"cookie"` and twenty-one more, `normalizeKey` (155-166) strips `-`, `_`, space and `.` before lowercasing, and `isSensitiveKey` (141-153) strips one leading `x`. A datastore column legally named `api_key` normalises to `apikey` and is stored as `"[redacted]"`.

The second rule is sharper. `headerPairIsSensitive` (129-139) finds an object with a `name` key whose *value* names a sensitive header, then rewrites the sibling `value` key. The use case the roadmap calls the most popular for this feature is a cross-run key-value store — columns `name` and `value`. A row `{"name": "cookie", "value": "chocolate chip"}` lands in the trace as `{"name": "cookie", "value": "[redacted]"}`, with no error anywhere.

The roadmap's other premises describe code that V2-p1-6 — `FEAT-1br8at`, done — already removed, and are corrected here. `looksLikeCredential` is gone; `redact.go:56-67` records why. `session`, `sessionid`, `otp` and `pin` are deliberately absent (41-46, 51-53). And `internal/webhook/webhook.go:252` redacts only the header map, so `{"pin":"482913"}` reaches the first node intact. What remains is the durable write and the live stream.

`internal/engine/service.go:140-187` marshals `run.Input` and `run.Output` verbatim into every node-run row and publishes the same bytes as the event `Data`. There is no per-node-type projection seam, and nothing prunes `execution_node_runs` — retention is V2-p6-3 and unbuilt — so a datastore row written once is copied into a trace that outlives the datastore, and a tenant's datastore delete does not delete that tenant's data. Isolation mirrors it: the catalogue carries `tenant_id`, while the physical table `kflow_ds_<16 hex>` carries only the user's columns plus `id`, `createdAt` and `updatedAt`.

All three matter for one reason. The posture on record is a datastore a host application reads with ordinary SQL inside a shared customer database. That holds only if the tenant boundary is something the catalogue enforces rather than something every call site remembers, if a deletion request is honoured in the trace as well as the table, and if a cell survives storage unmodified.

## Acceptance criteria

- [ ] A datastore row written through a workflow whose column is named `api_key` reads back byte-identical from the node-run trace and the live stream, proven by a test.
- [ ] A row shaped `{"name": "cookie", "value": "chocolate chip"}` survives the same round trip unrewritten, proven by a table-driven test naming the header-pair rule as the carved-out case.
- [ ] Credential material a node applied stays unreadable in a stored record, a node-run row, a live event and an API response, proven by the existing HTTP-credential end-to-end case still passing.
- [ ] A Datastore node run records row counts and row identifiers rather than cell contents, proven by a test asserting no written cell value appears in `execution_node_runs`.
- [ ] Reading a datastore owned by another tenant returns `repository.ErrNotFound` and never distinguishes absent from forbidden, proven by a test driving two `TenantScope` values.
- [ ] Every datastore repository entry point resolves a physical table only through the catalogue, so a caller-supplied table name is refused, proven by a test.
- [ ] Purging a tenant drops its physical tables, removes its catalogue rows and leaves no cell value in any surviving node-run row, captured as evidence on this ticket.
- [ ] A by-hand `make smoke-postgres` covering datastore create, write, cross-tenant read and purge is run and its output recorded on this ticket.

## Implementation Plan

Establish the blast radius first, because the roadmap's description of this defect is stale. Feed a datastore-shaped payload — one cell per column type, a `name`/`value` pair, a column called `api_key` — through `execution.Redact` in a test and record which cells die. That list scopes every decision below and stops the work re-fixing what V2-p1-6 already fixed.

**Provenance over an exemption list.** Two ways to carve datastore content out of redaction: mark a node run with the provenance of what produced it so the repository skips `Redact`, or teach `sensitiveKeys` an exemption for datastore column names. Reject the exemption list — it must be per-datastore, which makes a pure function in `internal/execution` depend on the catalogue and on a tenant scope it cannot reach, and a global exemption for `api_key` punches the same hole in the HTTP node's trace.

That is necessary and not sufficient: a datastore value read into `$json` is written again by the *next* node's trace, and a Set node reading `$json.api_key` carries no datastore provenance. Settle whether provenance travels along item lineage — V2-p1-2 supplies the paired-item machinery — or whether the downstream loss is accepted and documented.

Put the trace projection in the engine, not the node. `service.go:167` persists whatever the executor returned and `service.go:183` publishes the same bytes, so a node cannot record something other than what it emits. Add a per-node-type projection consulted once, before `CreateNodeRun` and before `publish`. Reject making the node return the summary as its output: the next node would receive counts instead of rows.

The trap is that redaction is silent, one-directional and lossy on write. Nothing records that `Redact` fired. A datastore holding `api_key` reads back correctly through the Datastore API — which never touches `payload` — and wrongly through the execution inspector, with no error in any log. A carve-out built at the wrong boundary passes every API test and surfaces months later in a support session, the traces already wrong.

Express isolation and purge through the catalogue, inside the transaction V2-p9-1 already orders metadata-first. A read resolves the public id to a surrogate under the caller's `TenantScope` and fails as `repository.ErrNotFound`, reproducing `internal/repository/workflows.go:25-26`. Reject a `tenant_id` column on each physical table: it is a second, unenforced copy of the truth that a host application would filter on while the catalogue disagreed. Purge walks the catalogue, drops the tables and deletes that tenant's executions and node runs — stopping at `DROP TABLE` states a guarantee it does not deliver.

**Recommend** recording row identifiers alongside counts rather than counts alone, because a failed multi-row write cannot otherwise be reconciled against the datastore — which is what V2-p1-5 and V2-p1-2 were made dependencies to enable. This reopens if a datastore is ever given a user-chosen primary key: the system `id` is opaque today, but a retype in V2-p9-3 or a natural upsert key in V2-p9-6 makes an identifier carry content, and identifiers become as sensitive as cells.

## References

- Roadmap plan, p9 section, entry V2-p9-12: `.pine/roadmap.md`.
- `internal/execution/redact.go` — `sensitiveKeys` (16-54), `headerPairKeys` and why value-prefix matching was removed (56-67), `headerPairIsSensitive` (129-139), `isSensitiveKey` and its leading-`x` strip (141-153), `normalizeKey` (155-166).
- `internal/repository/executions.go` — `payload` (605-611) applying `Redact` to every durable write, and `CreateNodeRun` (514-556) routing node input, output and error through it.
- `internal/events/events.go:139` — `Redact` on the live stream, inside `Publish`.
- `internal/webhook/webhook.go:233-282` — `requestPayload`, where line 252 redacts only the header map and the body is stored as it arrived.
- `internal/engine/service.go:140-187` — the node-run write and the paired publish, both carrying the executor's output verbatim; the seam a trace policy needs.
- `internal/repository/workflows.go:15-30` — `DefaultTenantID`, `TenantScope` and `ErrNotFound`, the isolation shape to reproduce for datastores.
- `internal/repository/models_test.go:303-306` — the cross-tenant read asserting `ErrNotFound`, the test shape this ticket owes.
- `.pine/tickets/FEAT-1br8at.md` — V2-p1-6, done; it removed `looksLikeCredential` and took `session`, `pin` and `otp` off the key list.

## Evidence — 2026-09-06 (DatastorePolicy slice)

- Provenance-over-exemption implemented as specified: no change to
  `sensitiveKeys`/`headerPairIsSensitive`. `internal/datastore/trace.go`
  (new) projects datastore node outputs to
  `{"datastore":{"ids":[...],"rows":N}}` (truncated flag past 100 ids);
  `internal/engine/trace.go` + 5-line `runOnce` seam apply it to the
  marshalled output before `CreateNodeRun` and the live publish, keyed by
  node type from the execution document (`datastore.NodeType =
  "kilasflow.datastore"`, pinned equal to `nodes.DatastoreNodeType` by
  `TestDatastoreTraceContractMatchesNodeType`). Inputs are deliberately not
  projected; downstream redaction loss is accepted and documented in
  `internal/engine/trace.go`.
- Redact untouched in behavior; doc note records the exclusion. New tests:
  `internal/execution/redact_datastore_test.go` (table-driven, names the
  sensitiveKeys vs headerPairIsSensitive rule per hostile shape),
  `internal/datastore/trace_test.go` (summary envelope, truncation,
  Redact fixed-point incl. alphabetical field order),
  `internal/engine/trace_test.go` (service-level: persisted node-run output
  AND live event carry only the summary; runner-level: full rows reach the
  next node in memory).
- Isolation: catalogue-scoped lookup already enforced tenant boundary; new
  `PurgeTenant` (`internal/datastore/isolation.go`) drops physical tables +
  catalogue rows in one tx. Tests: cross-tenant access across 14 entry
  points refuses with the existing `IsUnknown` ("unknown datastore")
  convention — deviation from the ticket's `repository.ErrNotFound`
  recorded: that sentinel would drag a datastore->repository import edge;
  the established `IsUnknown`->404 mapping (catalogue.go) covers it.
  Caller-supplied table names (incl. physical names, injection strings)
  refused; purge keeps neighbours and converges on retry.
- NOT done in this slice: deleting the tenant's executions/node-run rows on
  purge (repository layer, outside this ownership); by-hand
  `make smoke-postgres` (no PG in this environment — PG-only paths
  `pg_total_relation_size` and the `date_trunc` predicate are
  code-reviewed but unverified); downstream-provenance travel (accepted as
  documented loss per the ticket's own option).
