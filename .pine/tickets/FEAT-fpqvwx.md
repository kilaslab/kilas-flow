---
id: FEAT-fpqvwx
title: 'Tenant lifecycle: delete a customer and purge everything it owns'
status: done
priority: high
labels:
    - api
    - storage
    - security
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:31Z"
updated: "2026-09-21T00:31:12Z"
---

## Problem

A SaaS host cannot honour a data-deletion request. Two halves of a purge exist and neither is
called from anywhere, no API operation deletes a tenant, and most of a tenant's data is not
covered by either half.

## Evidence

- `internal/repository/tenant_purge.go:32` (`GORMExecutionStore.PurgeTenant`) deletes
  executions and node runs only. Callers: none outside its test.
- `internal/datastore/isolation.go:36` (`Engine.PurgeTenant`) drops the tenant's catalogue
  rows and physical tables. Callers: none outside tests.
- Not covered by either: `workflows`, `workflow_versions`, `workflow_publish_events`,
  `credentials`, `secret_bindings`, `schedules`, `webhook_bindings`, `webhook_routes`,
  `webhook_deliveries`, `execution_waits`, `users`, `api_keys`, and binary payload
  directories (`internal/binary/binary.go:176` — only per-execution deletion exists).
- `users` and `api_keys` reference `tenants(id)` `ON DELETE RESTRICT`
  (`migrations/*/000003_identity.up.sql`), so deleting the tenant row first fails.
- `webhook_deliveries` has no tenant column at all (`migrations/*/000001_baseline.up.sql`),
  so its rows are unreachable by a tenant purge even in principle.
- `datastore_columns` has no tenant column either; it is only safe today because every read
  is preceded by a tenant-scoped catalogue lookup.
- The operator surface (`internal/api/handlers/admin.go`) exposes list/get/create tenant,
  users and keys — no delete.

## Acceptance criteria

- [x] `DELETE /api/v1/tenants/{id}` on the operator surface, refused to any other principal,
      answering with what was removed.
- [x] One orchestrator deletes, in a single documented order: binaries, node runs, executions,
      waits, schedules, webhook deliveries/routes/bindings, secret bindings, credentials,
      workflow versions, publish events, workflows, datastore catalogue + physical tables,
      api keys, users, tenant row.
- [x] A test seeds two tenants across every table above, purges one, and proves the other's
      rows and cell values are intact and the purged tenant's are gone — including
      `webhook_deliveries`.
- [x] A retried delete converges (idempotent), and an empty tenant id is refused.
- [x] `webhook_deliveries` gains a tenant column (new migration, both dialects), and
      `datastore_columns` does too, so no future caller can cross tenants by id alone.

## Out of scope

Soft-delete/restore, export-before-delete, and per-tenant retention policy.

## Implementation notes

### Stage 1 — `tenant_id` on `webhook_deliveries` and `datastore_columns`

Migrations `000016_webhook_deliveries_tenant` and `000017_datastore_columns_tenant`
(sqlite + postgres, up + down) give both tables the tenant that owns the row and
backfill the rows already there. This is the last acceptance criterion, and it is
the only one this stage claims.

- 000016 attributes a delivery by the execution it queued, then the
  `webhook_routes` row, then the `webhook_bindings` row on its route, then — for a
  delivery whose route is really a legacy binding label — the binding with no
  route whose `path` equals it. A row none of those can attribute names a route
  that no longer exists; it is deleted rather than left with an empty tenant no
  purge could reach. The down migration does not restore it.
- 000017 copies the parent datastore's tenant onto each column row. Its delete of
  orphans is defence in depth: `datastore_id` is `ON DELETE CASCADE` to
  `datastores`, so with foreign keys enforced an orphan cannot exist.
- Both columns are `NOT NULL DEFAULT ''` on both dialects: SQLite cannot drop a
  column default without rebuilding the table, and the model tag `default:''` has
  to match the schema on both or the drift test fails on one. The guard against an
  empty tenant is therefore in Go, not in the schema.
- Indexes: `idx_webhook_deliveries_tenant (tenant_id)` and
  `idx_datastore_columns_tenant (tenant_id, datastore_id)`, the composite one
  serving both the tenant-scoped column read and the purge's delete by tenant.

Write and read paths now carry the tenant:

- `webhookDeliveryModel.TenantID`; `WebhookRepository.ClaimDelivery` and
  `RecordDeliveryExecution` take the tenant id. `ClaimDelivery` refuses an empty
  tenant (`repository.ErrTenantRequired`), refuses it only after the
  empty-route/empty-id no-op check, and filters its expired-clear, its insert and
  its duplicate read on the tenant; `RecordDeliveryExecution` refuses one and
  updates on it. The dedupe key stays `(route, delivery_id)`. The one consequence
  — a legacy path label two tenants share — is that the loser's claim errors and
  the handler runs the delivery instead: dedupe fails open, never across tenants.
- `datastoreColumnModel.TenantID`; `Create` and `AddColumn` stamp it, `Drop`,
  `RenameColumn` and `DropColumn` filter on it, and `columnsOf`,
  `columnsByDatastore` and `lookup` read through it, so a catalogue row naming a
  datastore its tenant does not own is invisible to every read.
- `internal/datastore/engine_test.go` re-arms the shared PostgreSQL schema by
  migration *name* (`datastores`, `datastore_columns_tenant`) rather than by
  `version = 5`: dropping `datastore_columns` and re-running only 000005 leaves a
  table without `tenant_id` under an applied 000017.

Verification, all from this worktree:

- `go test -race -count=1 ./internal/database/... ./internal/repository/... ./internal/datastore/... ./internal/webhook/...` — ok (SQLite).
- The same command with `KILASFLOW_TEST_POSTGRES_DSN` pointed at a scratch
  `pgvector/pgvector:pg17` container and `-p 1` — ok.
- New coverage: `TestTenantColumnMigrationsBackfillExistingRows{,OnPostgres}` (four
  deliveries attributed through the four routes, the orphan gone, zero empty
  tenants in both tables, both indexes present),
  `TestRollingBackTheTenantColumnMigrationsDropsTheColumnsAndKeepsTheTables{,OnPostgres}`,
  `TestMigratingAfterAPostgresRollbackRebuildsTheSchema`, the repository delivery
  tests (`TestClaimDeliveryRecordsTheTenantThatOwnsTheRoute`,
  `TestClaimDeliveryRefusesAnEmptyTenant`,
  `TestClaimDeliveryNeverReturnsAnotherTenantsExecution`,
  `TestRecordDeliveryExecutionIsTenantScoped`) and the datastore catalogue tests
  (`TestDatastoreColumnsCarryTheirTenant`, `TestColumnReadsAreTenantScoped`); every
  one runs both dialects and passes.
- Red first: the repository tests did not compile against the old signature, and
  `TestDatastoreColumnsCarryTheirTenant` reported "Create stamped 0 of 4 columns
  with the datastore's tenant". The harness fix was reproduced red by restoring the
  `version = 5` re-arm ("column \"tenant_id\" of relation \"datastore_columns\"
  does not exist") and green again with the name-based one.
- `gofmt -l` on every changed Go file prints nothing; `go vet ./...`; `go build ./...`;
  `go test -race ./internal/guardrails/...` — all clean. No config, API, SDK or
  generated artifact is touched by this stage.

### Stage 2 — the per-store purge primitives

This stage adds the three primitives the orchestrator will call and fixes the one
that was already there but broken. No acceptance criterion is ticked by it: the
ticket's criteria are about the deletion operation, and criteria 2–4 need the
orchestrator (stage 3) and the endpoint (stage 4) to exist first.

**Binary payloads (`internal/binary`).** `Store` gains
`DeleteTenant(tenantID string) (TenantResult, error)` with
`TenantResult{Executions, Files, Bytes}`. `FileStore.DeleteTenant` refuses an empty
or whitespace-only tenant (the highest-severity mistake available here:
`filepath.Join(root, "")` is the root, so it would have erased every tenant's
payloads), returns zero and nil for an id that fails `safeSegment` (`Put` refuses
the same predicate, so nothing can exist under such a name), locates the directory
by an **exact** entry-name match in `os.ReadDir(root)` rather than by joining the
id onto the root — `safeSegment` allows uppercase and macOS is case-insensitive, so
a join would let `DELETE /tenants/ACME` remove tenant `acme`'s payloads while every
database row naming `acme` survives — counts the top-level entries, regular files
and bytes with `WalkDir` (which never follows a symlink), then `RemoveAll`s the
matched entry. A missing directory is a zero result, so a retry converges.

**Repository rows (`internal/repository`).** `GORMExecutionStore.PurgeTenant` now
deletes node runs, **then waits**, then executions, and reports `Waits` as well:
`execution_waits.execution_id` is `ON DELETE RESTRICT`, so the old order failed the
whole transaction for any tenant with a suspended run — the ordinary case for a
deletion request. New `tenant_rows.go` adds `GORMTenantPurger` /
`NewTenantPurger`, one transaction per step, every method refusing an empty tenant
with `ErrTenantRequired` and returning a `map[string]int64` that includes a zero
for every table it handled:

- `LockOut` → `TenantLockOut{APIKeys, Users}`: `UPDATE api_keys SET revoked_at`
  and `UPDATE users SET disabled_at`, only for rows not already locked, so a retry
  counts zero and nothing is deleted.
- `ActiveTriggerWorkflows`: `SELECT DISTINCT workflow_id FROM webhook_bindings
  WHERE tenant_id = ?` — the list the coordinator is asked to stop before the
  credentials its triggers authenticate with are deleted.
- `PurgeTriggers`: schedules, webhook_deliveries, webhook_routes, webhook_bindings.
- `PurgeDefinitions`: secret_bindings, credentials, workflow_versions,
  workflow_publish_events, then workflows **`Unscoped()`** — `workflowModel`
  carries `gorm.DeletedAt`, so an ordinary delete reports a row it did not remove.
- `PurgeVectors`: every one of `vector_documents_384/768/1024/1536` and
  `vector_collections` guarded by `Migrator().HasTable` and named through
  `NamingStrategy.TableName` so `table_prefix` applies. On SQLite the four document
  tables do not exist, and on a PostgreSQL where migration 6 was skipped for a
  missing pgvector extension none of the five do, so an unguarded delete would
  abort the step on exactly the installations that never used vectors.
- `PurgeIdentity`: api_keys, users, then the tenant row (`WHERE id = ?`) — the
  order the `RESTRICT` foreign keys force.

**Datastore catalogue (`internal/datastore`).** `Engine.PurgeTenant` reads the
tenant's datastores **inside** the transaction that deletes them and keys every
delete on that read, deletes the tenant's mis-attributed `datastore_columns`
rows too (a column row naming a datastore this tenant does not own is invisible
to every read and would otherwise survive for ever — see the review round below
for how that is now expressed without the column delete being tenant-wide),
reports `Columns`, and drops physical tables with `DROP TABLE IF EXISTS` so a
purge interrupted after its DDL cannot wedge every retry on the table it removed
itself. *Superseded in review round 1: the read used to happen outside the
transaction, which could orphan a concurrently created datastore's table.*

Verification, all from this worktree:

- `go test -race -count=1 ./internal/binary/... ./internal/repository/... ./internal/datastore/...` — ok (SQLite).
- The same command with `KILASFLOW_TEST_POSTGRES_DSN` pointed at a scratch
  `pgvector/pgvector:pg17` container and `-p 1` — ok (the vector tables are absent
  there, so `TestPurgeVectorsToleratesAbsentVectorTables` took its guarded path and
  said so in the log).
- Red first, each for the right reason: the binary tests did not compile against
  the old `Store`; `TestPurgeTenantRemovesWaitsBeforeExecutions` failed with
  `purge tenant executions: constraint failed: FOREIGN KEY constraint failed (1811)`;
  `TestPurgeTenantDropsTablesAndKeepsNeighbours` failed with
  `no such table: ds_…` once one physical table was dropped by hand.
- The new tests were each shown to bite by mutating the implementation:
  removing `Unscoped()` left the soft-deleted workflow behind
  (`workflows:1` after the purge), removing the `HasTable` guard produced
  `no such table: vector_documents_384`, and restoring the `datastore_id IN ids`
  delete left the mis-attributed column row (`Columns:2`, want 3).
- `gofmt -l` on every changed Go file prints nothing; `go vet ./...`; `go build ./...`;
  `go test -race ./internal/guardrails/...` — all clean. No config, API, SDK,
  migration or generated artifact is touched by this stage; no new dependency.

Harness notes: `eachDriver`'s PostgreSQL cleanup now deletes `execution_waits`
(before executions, because of the `RESTRICT` foreign key) plus the trigger,
definition and identity tables these tests seed, and the datastore purge test
tracks the neighbour's physical table so the shared server does not accumulate
`ds_…` leftovers.

### Stage 3 — the `tenantpurge` orchestrator, the seeding test and the schema-driven completeness test

New package `internal/tenantpurge` (nothing imports it yet; stage 4 wires the
composition root and the endpoint to it):

- `doc.go` — the documented order, why each position is forced, one transaction
  per step with what that costs on each dialect, the retry contract, and why the
  production purge never introspects the schema.
- `purge.go` — `Service`, `Deps`, `Purge`, `Steps`, `Tables`, `Exempt`,
  `Result`, `StepError`, `ErrTenantRequired`, `ErrProtectedTenant`, and the
  small interfaces it calls (`RunPurger`, `RowPurger`, `DatastorePurger`,
  `BinaryPurger`, `SessionForgetter`, `TriggerStopper`). `New` refuses a nil
  run, row or datastore purger; a nil binary store, session memory or trigger
  stopper is a deployment decision and those steps are skipped rather than
  failed.
- `purge_test.go`, `completeness_test.go`, `harness_test.go` (the dialect
  harness, the live-schema introspection and the seeding helpers shared by both
  test files).

The order, and why each position is not a preference:

| step | tables | why it is here |
| --- | --- | --- |
| `lock-out` | `api_keys`, `users` (UPDATE only) | the tenant cannot write while its own rows are going; a purge that fails leaves it locked out, which is the safe direction |
| `stop-triggers` | — | `webhook.Coordinator.Deactivated` reads the tenant's bindings and its hooks resolve the tenant's credentials, so both have to still exist |
| `triggers` | `schedules`, `webhook_deliveries`, `webhook_routes`, `webhook_bindings` | closes intake; a webhook arriving later would queue an execution the definitions step then cannot delete (workflow_versions RESTRICT) |
| `binaries` | — | a filesystem has no transaction to join, so it is a step of its own; the directory goes before the rows naming its payloads |
| `runs` | `execution_node_runs`, `execution_waits`, `executions` | waits precede executions (`execution_waits.execution_id` is RESTRICT) |
| `definitions` | `secret_bindings`, `credentials`, `workflow_versions`, `workflow_publish_events`, `workflows` | versions before workflows (RESTRICT); workflows are hard-deleted |
| `sessions` | — | in-process conversation memory; best effort by construction |
| `datastores` | `datastore_columns`, `datastores` | catalogue rows and the physical tables they name, together |
| `vectors` | `vector_documents_384/768/1024/1536`, `vector_collections` | every table guarded, since a SQLite or non-pgvector server has none of the five |
| `identity` | `api_keys`, `users`, `tenants` | forced by both RESTRICT foreign keys; the tenant row is last, and is the record that the purge finished |

`Result.Removed` names **every** covered table — 22 names — including the ones a
call found empty, so a caller can tell "nothing was there" from "not covered".
The lock-out step's UPDATE counts are not removals and go to the log only;
`DatastoreTables` and `Binaries` carry the two counts that are not table rows.
A failure returns the partial `Result` plus a `*StepError` naming the step;
steps after the failure are not attempted. The context is checked between steps,
so a cancelled purge stops at a boundary and says where (the caller detaches the
purge from the request deadline before calling it).

The completeness proof is schema-driven, as the parallel-work map requires:
`TestEveryTenantTableIsPurgedOrExplicitlyExempt` introspects the live schema
(`sqlite_master` + `pragma_table_info`, or `information_schema.columns`),
restricts the result to tables this dialect's embedded migrations declare (the
regexp parser is local because `internal/database`'s is unexported and the
PostgreSQL server is shared), and fails with the table name and a fix hint for
anything not in `Tables()` or `Exempt()`. `Exempt()` is empty today; its reasons
are asserted non-empty and to name a real table, and every covered table is
asserted to exist in the dialect's migrations except the four PostgreSQL
document tables.

Verification, all from this worktree:

- `go test -race -count=1 ./internal/tenantpurge/` — ok (SQLite).
- The same with `KILASFLOW_TEST_POSTGRES_DSN` pointed at a scratch
  `pgvector/pgvector:pg17` container (`kf-pg-feat-fpqvwx`, port 32794, removed
  afterwards) and `-p 1` — ok, every test in both dialects:
  `TestEveryTenantTableIsPurgedOrExplicitlyExempt{,/sqlite,/postgres}`,
  `TestPurgeRemovesOneTenantAndLeavesTheOtherIntact{,/sqlite,/postgres}`,
  `TestPurgeHonoursTheTablePrefix`,
  `TestPurgeStopsTriggersBeforeTheirRowsAndCredentialsAreDeleted{,/sqlite,/postgres}`,
  `TestPurgeRunsTheStepsInTheDocumentedOrder`,
  `TestPurgeRefusesEmptyWhitespaceAndProtectedTenants`,
  `TestNewRefusesAMissingCollaborator`,
  `TestAFailedStepStopsThePurgeAndARetryConverges{,/sqlite,/postgres}`,
  `TestPurgeStopsAtAStepBoundaryWhenTheContextIsCancelled`.
- The vector tables **were** present on the recorded PostgreSQL run: the seeding
  test logged `seeded and asserting 21 tenant_id tables: […] (vector documents on
  this server: [vector_documents_384 vector_documents_768 vector_documents_1024
  vector_documents_1536])`. Reaching that state needed the prerequisite the plan
  named: a fresh pgvector container has the extension *available but not
  installed*, so 000006 is skipped, no vector table exists, and the vector
  coverage would have passed vacuously. The harness runs `CREATE EXTENSION IF
  NOT EXISTS vector`, re-arms migration 6 **by name** (`vector_store`), and
  migrates again; without a role that can install it, it logs loudly and seeds
  and asserts only the tables that exist.
- SQLite coverage: `seeded and asserting 17 tenant_id tables: [api_keys
  credentials datastore_columns datastores execution_node_runs execution_waits
  executions schedules secret_bindings users vector_collections webhook_bindings
  webhook_deliveries webhook_routes workflow_publish_events workflow_versions
  workflows]` — the four document tables are absent by design.
- The seeding test is a real end-to-end purge: real stores, real datastore
  engine, real `binary.FileStore` under `t.TempDir()`, real session memory,
  tenant A and B seeded through the public APIs (drafts, activation with webhook
  and schedule extraction, delivery claims, credentials and secret bindings,
  datastore cells, payloads, users and keys, a soft-deleted workflow, an
  execution with a raw node run and a raw wait, and on PostgreSQL a vector
  collection plus a document per dimension). Before the purge every
  introspected table is asserted to hold at least one row for **each** tenant,
  so a seed that forgot a table cannot make the rest pass vacuously; after it,
  A is zero everywhere, B's counts are identical, and B's datastore cell, traced
  cell and payload bytes read back by value. A cleanup purges B through the
  service and asserts zero, so a crashed run leaves nothing.
- Red first: the package had no non-test Go files, so the tests did not compile
  until `purge.go` existed; then
  `TestAFailedStepStopsThePurgeAndARetryConverges/postgres` failed with
  `the retry called [lock-out stop-triggers:list triggers …]` — correctly, as it
  turned out: the retry has no bindings left to stop, so the expectation was the
  wrong one and the test now records why.
- Every new guard was shown to bite by mutating the implementation, then
  restoring:
  - moving `stop-triggers` after `triggers` →
    `Purge() called the collaborators as [lock-out triggers stop-triggers:list …]`
    and `Steps() = [lock-out triggers stop-triggers …]`; the same mutation makes
    `TestPurgeStopsTriggersBeforeTheirRowsAndCredentialsAreDeleted` report
    `the purge stopped triggers for [], want exactly the tenant's active
    workflows [wf_pt-stop-…]`;
  - a temporary `migrations/sqlite/000015_idempotency_keys` standing in for
    FEAT-hj8pyx's table →
    `the sqlite schema has a tenant_id table the purge does not cover:
    idempotency_keys. Delete its rows in the step that owns them in
    internal/tenantpurge/purge.go, or add it to Exempt() …` (and with an
    `Exempt()` entry the test passes; with an empty reason it fails with
    `idempotency_keys is exempted from the purge with an empty reason`);
  - removing the seeding of `Result.Removed` with the covered tables →
    `Purge() reported 18 tables, want every covered table (22)`.
  The temporary migration and every mutation were removed again; `git status`
  shows only the new package and this ticket file.
- `gofmt -l` on the changed files prints nothing; `go vet ./...`; `go build ./...`;
  `go test -race -count=1 ./internal/guardrails/...` — all clean. No migration,
  config, API, SDK or generated artifact is touched by this stage; no new
  dependency, so no licence change.

Notes for stage 4: `Service.Tables()` and `Service.Steps()` are the coverage
contract a docs-order test can compare the page against, so the page has to use
the step names above. `Purge` honours the context between steps but does not
detach itself from it: the endpoint is expected to call it with a context that
outlives the request (a client that gives up after its own timeout must not
cancel a deletion that is still converging). A nil `Logger` falls back to
`slog.Default()`.

### Stage 4 — the operator endpoint, the composition root, the SDK and the page

This stage proves the last open criterion. It adds the surface the two earlier
halves were built for, and nothing about the purge order changes.

**Handler (`internal/api/handlers/admin.go`).** `TenantPurger`
(`Purge(ctx, tenantID) (tenantpurge.Result, error)`), `WithTenantPurger`,
`deleteTenantInput` (`id` path parameter, `minLength:"1"`, `maxLength:"64"`),
`TenantDeletionResource` and the **named** `BinaryRemoval` type the generated
schema needs a stable name for. `DeleteTenant` refuses in the order that makes
each refusal mean something: `operatorOnly(ctx)` first (so a customer's key
cannot start a deletion or learn what exists), then a nil purger answers `503`
"tenant deletion is not configured on this instance", then the purge. Errors are
mapped as the plan specifies — `ErrTenantRequired` → 422,
`ErrProtectedTenant` → 409, `*StepError` → 500 whose detail names the step and
says the request can be repeated, everything else → 500 — and the driver text
never reaches the body (`serverProblem` logs it against the request id instead).
The purge runs on `context.WithoutCancel(ctx)`; the original context is what the
log line and `serverProblem` use. On success it logs at Info with the tenant, the
principal's key ID and the `Removed` map, and answers 200. `removed` is
normalised to `{}` when a purger reports nil, so a client iterating it never sees
`null`.

**Composition root.** `internal/api/routes.go` gains one call:
`.WithTenantPurger(deps.TenantPurger)`. `internal/api/server.go` gains
`Deps.TenantPurger handlers.TenantPurger`, documented as nil-answers-503.
`cmd/kilasflow/main.go` hoists the coordinator that used to be written inline
inside the `api.Deps` literal into `triggerCoordinator` and hands it to both
`TriggerCoordinator` and the purge; builds `repository.NewTenantPurger(db.DB)`
and `tenantpurge.New` with the node-catalogue lifecycle closure
(`definition.LifecycleID != ""`, the same read `Workflows.lifecycleIDs` does) and
`Protected: []string{repository.OperatorTenantID}`; returns the error; and adds
`TenantPurger` to the Deps literal. All of it sits after the worker-only early
return, so a worker process builds none of it. `binaries` stays the possibly-nil
`binary.Store` interface, so it converts to a nil `BinaryPurger` and the payload
step is skipped on an install with no binary root — the typed-nil trap the plan
warned about does not apply because the value is an interface, not a pointer in
one.

**Tests.** `internal/api/handlers/admin_admin_test.go`: `DELETE /tenants/{id}` is
part of `adminOperations(...)`, so both refusal tests cover it; the fake purger
increments the shared `adminTestStore.calls`, which is what keeps the embed
test's "the request reached the store" control true for an operation that never
touches identity persistence; and six new tests
(`TestAdminOperatorDeletesATenantAndSeesWhatWasRemoved` including the retry and
the `"removed":{}` shape, `…RefusesTheOperatorTenantWith409`,
`…RefusesAnEmptyTenantWith422`, `…Answers503WithoutAPurger`,
`…FailureNamesTheStepAndLeaksNoDriverText`,
`…ContinuesAfterTheRequestContextIsCancelled`).
`internal/api/tenant_delete_test.go` is the end-to-end proof over real SQLite
with real stores and a real `tenantpurge.Service` behind `api.NewServer` and
auth enabled: a customer's key is refused 403, the operator key deletes one of
two tenants and the response carries the counts (workflow, version, datastore
rows and the dropped physical table), the listing no longer holds it, the
sibling's workflow still reads back by value with its own key, the deleted
tenant's key answers 401, the physical datastore table is gone, and the repeat
answers 200 with zeros and `tenantRemoved: false`. Two further tests cover an
unknown id (200, every covered table at zero) and the operator's own tenant
(409 with the tenant still present).
`internal/tenantpurge/docs_test.go` (`TestDocumentedOrderMatchesTheOrchestrator`)
reads the new page and asserts every step name appears in `Steps()` order (first
appearance, so a later mention is fine) and every covered table is named on it.

**SDK.** `deleteTenant(tenantId, signal?)` in the Tenants block of
`sdk/src/server.ts`, with the doc comment the plan asks for (irreversible,
operator credential, idempotent, and that a large tenant can outlast the default
`timeoutMs` of 30s — the server keeps going and the call is repeated until it
reports zeros). `sdk/test/operation-coverage.test.mjs` maps
`'delete-tenant': 'deleteTenant'`; `sdk/test/operations.test.ts` asserts the
method, the percent-encoded path (`acme/../globex` → `acme%2F..%2Fglobex`) and
the typed result; `sdk/README.md` lists it in the Tenants and accounts row;
`sdk/CHANGELOG.md` gains an `## Unreleased` heading above 0.1.0 with an
**Additive** entry. No version bump.

**Generated artifacts.** `scripts/generate-api-reference.mjs` gained
`'delete-tenant'` in the curated tenants group (the run fails naming any
unmapped operation, so this is what keeps a new endpoint documented). Then
`make generate-api generate-types generate-api-reference`:
`web/src/lib/api/generated/**` (the admin client plus
`binaryRemoval.ts`/`tenantDeletionResource{,Removed}.ts`),
`sdk/src/generated/models.ts` and
`docs/src/content/docs/reference/api/tenants.md` + `api.md`, whose count is now
75 operations.

**Docs.** New page `docs/src/content/docs/operate/tenant-deletion.md`: what it
does and that it is irreversible, the ordered steps each named in backticks, why
waiting rows precede executions and why intake closes first, why the identity
foreign keys stay `RESTRICT` and the tenant row goes last, per-dialect
transactional behaviour (one transaction per step; DDL transactional on both,
`DROP TABLE` taking `ACCESS EXCLUSIVE`, SQLite's single connection), retry
semantics and the lock-out, a curl example using
`Authorization: Bearer $KILASFLOW_AUTH_OPERATOR_KEY` (the default of
`auth.operator_key_env`) and the note that the endpoint is unreachable with
authentication disabled, the `<binary.root>/<tenant>/<execution>/<id>` layout and
the `table_prefix` note, how to verify with the counts, and a plainly stated list
of what it does not remove. `docs/src/content/docs/reference/api-contract.md`
line 41 goes from 74 to 75 operations, and `CHANGELOG.md` gains an `[Unreleased]
→ Added` entry. Neither `concepts/tenancy-and-embedding.md` nor
`guides/community-nodes.md` is touched (BUG-vzzkg3 owns them); the two sentences
that need changing are in this stage's report.

**Red first.** The new handler tests did not compile: `.WithTenantPurger
undefined`, `DeleteTenant undefined`, `deleteTenantInput undefined`,
`BinaryRemoval undefined`. Each guard was then shown to bite by mutating the
implementation and restoring it:

- swapping `` `lock-out` `` and `` `triggers` `` on the docs page →
  `the page names "stop-triggers" before "lock-out", but Steps() runs them the
  other way round`;
- passing the request context straight to `Purge` instead of
  `context.WithoutCancel(ctx)` → `Purge() saw a cancelled context, want the
  deletion detached from the request's deadline`;
- removing `'delete-tenant': 'deleteTenant'` from the SDK coverage map →
  `operations with no client method and no exclusion: delete-tenant`.

**Commands run, and outcomes** (all from this worktree):

- `go test -race -count=1 ./internal/api/... ./internal/tenantpurge/... ./cmd/...
  ./internal/guardrails/...` — ok (SQLite), including the new
  `internal/api` end-to-end tests and `TestDocumentedOrderMatchesTheOrchestrator`.
- `gofmt -l` on every changed Go file prints nothing; `go vet ./...` and
  `go build ./...` clean; `sh scripts/check-coordinates.sh` clean.
- `make generate-api generate-types generate-api-reference`, then
  `make generate-api-check generate-types-check generate-api-reference-check
  sdk-check sdk-test sdk-version-check` — all clean; the reference check reports
  14 pages fresh, 75 operations.
- `make sdk-test` — 6 files, 82 tests passed (the operation-coverage gate boots a
  real binary, so it fails until the Go operation exists — verified by removing
  the map entry).
- `cd web && pnpm install --frozen-lockfile && pnpm check && pnpm test` — 0 errors
  from 1520 files, 514 tests passed.
- `cd docs && pnpm install --frozen-lockfile` and `make docs-build` — 44 pages,
  all internal links valid.
- **By hand, against a real binary** (`go build ./cmd/kilasflow`, started on
  SQLite with `auth.enabled`, the signing key and
  `KILASFLOW_AUTH_OPERATOR_KEY`): `POST /api/v1/tenants` 201,
  `POST /api/v1/tenants/acme/api-keys` 201, then `DELETE
  /api/v1/tenants/acme` with the **customer** key → `403 this endpoint is
  reserved for the operator`, and with the **operator** key → `200` with the
  full `removed` map (`api_keys:1`, `tenants:1`, every other covered table named
  at zero), `datastoreTables:0`, `binaries` zeros. The repeat answered `200` with
  `tenantRemoved:false` and no non-zero count; the deleted tenant's key then
  answered `401`; `DELETE /api/v1/tenants/operator` answered `409 the operator
  tenant cannot be deleted`; and `GET /api/schemas/BinaryRemoval.json` served the
  named schema, which is what gives the SDK a stable type.
- PostgreSQL (scratch `pgvector/pgvector:pg17` container named
  `kf-pg-feat-fpqvwx`, port 32800, `CREATE EXTENSION vector` in `kilasflow` and
  `template1` as the plan's prerequisite requires, removed afterwards):
  `KILASFLOW_TEST_POSTGRES_DSN=… go test -race -p 1 -count=1 ./internal/api/...
  ./internal/tenantpurge/... ./cmd/...` — `internal/tenantpurge` ok (112.9s),
  `cmd/kilasflow` ok, `internal/api/middleware` ok. On the first pass
  `internal/api/handlers` failed with
  `--- FAIL: TestLoginRefusesASprayFromOneAddress (199.56s) auth_test.go:162: no
  refusal within 30 attempts`, which is BUG-fng4m2's wall-clock-dependent
  sign-in throttle test — 30 attempts spread over 199s refill faster than they
  spend, so it cannot pass at ≈9 attempts/min. It is unrelated to this change
  (it exercises `Auth.Login` and the in-memory limiter; `auth_test.go` is not in
  this diff), the machine was at load average 27 with 15 concurrent sibling test
  binaries, and it passes both in isolation on this branch (153.5s) and in a
  full re-run of the package on the same scratch server (132.1s). No test was
  weakened, skipped or deleted.

The order deviations recorded in stage 3 (waits before executions, trigger rows
right after `lock-out`/`stop-triggers` rather than after `runs`, plus the
lock-out, stop-triggers, vector and session steps) are unchanged by this stage;
the ticket's literal order line is superseded by the STEPS table above and by
`Service.Steps()`. The vector-table gap and its extension prerequisite — a fresh
`pgvector/pgvector:pg17` has the extension available but not installed, so
migration 000006 is skipped and the vector coverage would pass vacuously — are
handled by the stage-3 harness, which installs it, re-arms migration 6 by name
and logs loudly when the role cannot. The by-hand failing runs of the
completeness test and the adjacent defects deliberately not fixed here
(`PruneExpired` and the missing `Deactivated` on single-workflow deletion) are
recorded in stage 3 and repeated in this stage's report.

### Review round 1 — the fixer

Two findings came out of the three-lens review of stage 4: one HIGH in the
destructive path and one low in the page.

**HIGH — the datastore purge could orphan the table of a datastore created while
it ran (`internal/datastore/isolation.go`).** `PurgeTenant` listed the tenant's
datastores on the outer handle, built the drop list from that read, and only then
opened the transaction that deleted the catalogue rows *by tenant*. A datastore
created for the tenant between the read and the delete — a worker executing a
datastore node's create operation, or a session inside the middleware's
revalidation window — therefore had its `datastores` and `datastore_columns` rows
deleted while its physical table was never dropped. Nothing later could repair
it: the read that names a purge's tables goes through the catalogue, the
catalogue no longer named that table, and every retry reported zeros over cells
that were still on disk.

The invariant, restated precisely: **the rows a purge deletes are the rows it
read, and the tables it drops are the tables those rows name.** A datastore
committed before the read is dropped and counted; one committed after it keeps
its row, its columns **and** its table, and the next call converges on it. Two
details follow from that and are worth writing down because the obvious
implementation gets them wrong:

- Deleting by `WHERE tenant_id = ?` inside the transaction is *not* enough. Under
  READ COMMITTED (and on SQLite, whose snapshot the delete statement takes when it
  runs) a row committed after the read is still visible to the delete, so the
  orphan is created inside the transaction instead of outside it. The datastore
  delete is keyed on the ids the read returned.
- The column delete could not simply become `datastore_id IN (ids)`: a column row
  naming a datastore this tenant does not own is invisible to every read and would
  then survive for ever, which is what stage 2 deliberately fixed. It is now two
  statements — the read datastores' columns by their ids, then the tenant's
  leftovers (`datastore_id NOT IN (SELECT id FROM datastores WHERE tenant_id = ?)`,
  which excludes a datastore created while the purge ran) — and `Columns` is the
  sum of the two, so the stage-2 count of 3 for the mis-attributed row is
  unchanged.

The read runs on `tx`, not on the outer handle, because SQLite's handle carries
one connection and `tx` is holding it.

Proof — `TestPurgeTenantNeverOrphansATableCreatedWhileItRuns`
(`internal/datastore/isolation_test.go`), on SQLite and PostgreSQL. The interleave
is deterministic, not a create loop racing the purge: the hook fires when the
purge composes its first `DROP` (the moment it has committed to the tables it
read) and the create is committed by a second handle on the same database before
the purge runs another statement. It asserts that no datastore table created
during the test exists without a `datastores` row anywhere, that the purge
reports exactly the datastores it read, that a purge which succeeded left the
rival whole, and that the repeat converges to no table, no row and no column.

- Red first, against the current code, both dialects — the orphan is real:
  `KILASFLOW_TEST_POSTGRES_DSN=… go test -race -count=1 -run
  TestPurgeTenantNeverOrphansATableCreatedWhileItRuns ./internal/datastore/`
  → `physical table "ds_9c44a60a2ad7b4a4" has no datastores row naming it, so no
  purge can reach it` and the same for `ds_322c63f11825d4f1` on PostgreSQL, plus
  `PurgeTenant (repeat) = {Datastores:0 Tables:[] Columns:0}`.
- Green with the fix: `go test -race -count=1 -run TestPurgeTenant
  ./internal/datastore/` — ok on both dialects, the pre-existing
  `TestPurgeTenantDropsTablesAndKeepsNeighbours` included (its `Columns: 3`
  assertion still holds).
- The dialect difference the test tolerates is observed and reported, not hidden:
  on SQLite the purge refuses its own write once the create has committed on the
  other connection (`datastore: drop table for purge: database is locked (517)`,
  SQLITE_BUSY_SNAPSHOT), rolls back whole, and the repeat converges; on
  PostgreSQL the purge succeeds and leaves the rival for the next call. The test
  logs which of the two happened. Both are invariant-preserving, and the old
  behaviour in that window — report success, orphan the table — was not.
- `testDriver` gained `driver`/`dsn` (`internal/datastore/engine_test.go`) so the
  test can open the second handle without the migration re-arm `open` performs,
  and the SQLite dsn is now one file per test because `t.TempDir()` returns a new
  directory per call. No existing test changes behaviour: none called `open`
  twice in one test.

**Low — the page's response example listed 18 keys where the endpoint reports 22
(`docs/src/content/docs/operate/tenant-deletion.md`).** The four
`vector_documents_*` widths are seeded into `Result.Removed` for every dialect, so
they answer `0` even where no such table exists, and the example omitted them
while the prose on the same page said `removed` names every covered table. The
example now carries the four keys, the paragraph says why they are zero, and the
transactional section states the datastores step's read-and-drop invariant.
`internal/tenantpurge/docs_test.go` gained
`TestDocumentedResponseExampleNamesEveryTableTheServiceReports`, which parses the
page's JSON example and compares its key set with `Service.Tables()` in both
directions. Shown to bite by deleting the `vector_documents_384` line from the
example: `the response example is missing "vector_documents_384", which every
purge reports`; restored, green.

Gates for this round (details in the fixer's report): `gofmt -l` clean, `go vet
./...`, `go build ./...`, `go test -race -count=1` on `internal/datastore`,
`internal/tenantpurge`, `internal/repository` and `internal/api`, both dialects,
`go test ./internal/guardrails/...`, and `make docs-build`.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `794adbb3` (last commit at or before ticket created 2026-09-20)
- Commits (3):
  - `7b9d4752` — FEAT-fpqvwx: delete a tenant and purge everything it owns
  - `b3c9f3f9` — chore(pine): start the board — the open tickets move to doing before the parallel wave
  - `f8156140` — chore(pine): record the ticket board — the new tickets, memory entries and their notes
- Files changed (base → working tree):

```
 .github/actions/js-toolchain/action.yml            |   11 +-
 .github/workflows/ci.yml                           |   25 +-
 .github/workflows/release.yml                      |  120 +-
 .pine/MEMORY.md                                    |    1 +
 .pine/memory/embedding.md                          |    8 +
 .pine/tickets/BUG-fng4m2.md                        |   88 ++
 .pine/tickets/BUG-fvdz46.md                        |  400 +++++++
 .pine/tickets/BUG-p3t7yq.md                        |   30 +
 .pine/tickets/BUG-t9j2ek.md                        |  452 ++++++++
 .pine/tickets/BUG-vzzkg3.md                        |   54 +
 .pine/tickets/BUG-w8h3km.md                        |  123 ++
 .pine/tickets/BUG-xmr673.md                        |  737 ++++++++++++
 .pine/tickets/EPIC-bkj6yf.md                       |   46 +
 .pine/tickets/EPIC-m0bne8.md                       |   57 +
 .pine/tickets/EPIC-r0yg5q.md                       |   27 +
 .pine/tickets/FEAT-3taswf.md                       | 1181 +++++++++++++++-----
 .pine/tickets/FEAT-48hreg.md                       |   20 +-
 .pine/tickets/FEAT-4jns31.md                       |   26 +
 .pine/tickets/FEAT-4ve1bq.md                       |   67 ++
 .pine/tickets/FEAT-77rveq.md                       |   73 ++
 .pine/tickets/FEAT-7cg0cd.md                       |   20 +-
 .pine/tickets/FEAT-8mymac.md                       |    4 +-
 .pine/tickets/FEAT-bb4s6e.md                       |   27 +
 .pine/tickets/FEAT-bp59m4.md                       |  420 +++++++
 .pine/tickets/FEAT-c72set.md                       |   26 +
 .pine/tickets/FEAT-cwmw90.md                       |  513 ++++++++-
 .pine/tickets/FEAT-emf6k5.md                       |  573 ++++++++++
 .pine/tickets/FEAT-ew46cb.md                       |   26 +
 .pine/tickets/FEAT-fpqvwx.md                       |  616 ++++++++++
 .pine/tickets/FEAT-g07pj8.md                       |   67 ++
 .pine/tickets/FEAT-hj8pyx.md                       |  750 +++++++++++++
 .pine/tickets/FEAT-m4d2y1.md                       |   30 +
 .pine/tickets/FEAT-mha6a0.md                       |  259 +++++
 .pine/tickets/FEAT-p77zr3.md                       |   67 ++
 .pine/tickets/FEAT-qdedm0.md                       |  993 +++++++++++++++-
 .pine/tickets/FEAT-x5qqpm.md                       |   26 +
 .pine/tickets/FEAT-yxwyav.md                       |   26 +
 CHANGELOG.md                                       |   98 +-
 CONTRIBUTING.md                                    |    3 +
 Makefile                                           |   53 +
 README.md                                          |   11 +-
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 +++
 cmd/kilasflow/fleet.go                             |   92 ++
 cmd/kilasflow/fleet_test.go                        |  395 +++++++
 cmd/kilasflow/idempotency_test.go                  |  225 ++++
 cmd/kilasflow/main.go                              |  198 +++-
 cmd/kilasflow/webhook_wiring_test.go               |  138 +++
 config.example.yaml                                |   67 +-
 docs/src/content/docs/concepts/credentials.md      |   21 +-
 docs/src/content/docs/concepts/execution-model.md  |    1 +
 docs/src/content/docs/concepts/node-registry.md    |   40 +
 docs/src/content/docs/concepts/webhooks.md         |  107 +-
 docs/src/content/docs/guides/embedding.md          |   32 +-
 docs/src/content/docs/guides/idempotency.md        |  197 ++++
 docs/src/content/docs/guides/node-authoring.md     |    6 +
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 +++
 .../docs/operate/configuration-reference.md        |  111 +-
 docs/src/content/docs/operate/deployment.md        |   10 +-
 docs/src/content/docs/operate/security.md          |   23 +-
 docs/src/content/docs/operate/tenant-deletion.md   |  194 ++++
 docs/src/content/docs/operate/upgrades.md          |   61 +-
 docs/src/content/docs/reference/api-contract.md    |   34 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/datastores.md  |    6 +-
 docs/src/content/docs/reference/api/errors.md      |   13 +-
 docs/src/content/docs/reference/api/nodes.md       |   16 +-
 docs/src/content/docs/reference/api/system.md      |    3 +-
 docs/src/content/docs/reference/api/tenants.md     |   21 +
 docs/src/content/docs/reference/api/workflows.md   |   24 +-
 docs/src/content/docs/reference/cli.md             |  628 +++++++++++
 docs/src/content/docs/reference/node-packs.md      |   15 +-
 docs/src/content/docs/start/install.md             |   12 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   11 +-
 .../specs/2026-09-20-agent-surface-design.md       |  519 +++++++++
 e2e/fixtures/live-backend.ts                       |   12 +-
 e2e/helpers/stub.ts                                |    8 +
 e2e/tests/dashboard-lists.spec.ts                  |  187 ++++
 e2e/tests/live-backend-api.spec.ts                 |   52 +-
 e2e/tests/live-backend-http-auth.spec.ts           |   94 ++
 e2e/tests/live-backend-webhook.spec.ts             |  117 +-
 go.mod                                             |    2 +
 go.sum                                             |    4 +
 internal/api/cors_test.go                          |    4 +-
 internal/api/embed_defaults_test.go                |  150 +++
 internal/api/handlers/admin.go                     |  154 +++
 internal/api/handlers/admin_admin_test.go          |  263 ++++-
 internal/api/handlers/auth_test.go                 |   63 +-
 internal/api/handlers/datastores.go                |  183 ++-
 internal/api/handlers/idempotency.go               |  115 ++
 internal/api/handlers/interop.go                   |    6 +-
 internal/api/handlers/nodes.go                     |   76 +-
 internal/api/handlers/problem.go                   |   17 +
 internal/api/handlers/system.go                    |  142 ++-
 internal/api/handlers/workflows.go                 |  166 ++-
 internal/api/idempotency_test.go                   | 1004 +++++++++++++++++
 internal/api/middleware/cors.go                    |   11 +-
 internal/api/middleware/cors_test.go               |    2 +-
 internal/api/middleware/embed.go                   |    3 +-
 internal/api/middleware/loginlimit.go              |   13 +
 internal/api/middleware/loginlimit_test.go         |    3 +-
 internal/api/node_visibility_test.go               |  544 +++++++++
 internal/api/ready_fleet_test.go                   |  358 ++++++
 internal/api/routes.go                             |   16 +-
 internal/api/server.go                             |   13 +-
 internal/api/tenant_delete_test.go                 |  386 +++++++
 internal/api/workflows_test.go                     |   71 +-
 internal/binary/binary.go                          |  117 ++
 internal/binary/binary_test.go                     |  217 ++++
 internal/cli/api_prefix.go                         |   43 +
 internal/cli/cli.go                                |  382 +++++++
 internal/cli/cli_test.go                           |  248 ++++
 internal/cli/client.go                             |  397 +++++++
 internal/cli/client_test.go                        |  320 ++++++
 internal/cli/command.go                            |  139 +++
 internal/cli/command_test.go                       |  302 +++++
 internal/cli/config.go                             |  258 +++++
 internal/cli/config_test.go                        |  749 +++++++++++++
 internal/cli/context.go                            |  318 ++++++
 internal/cli/context_test.go                       |  236 ++++
 internal/cli/doc.go                                |   35 +
 internal/cli/exit.go                               |  113 ++
 internal/cli/exit_test.go                          |  101 ++
 internal/cli/flags.go                              |   84 ++
 internal/cli/guard.go                              |   43 +
 internal/cli/guard_test.go                         |  212 ++++
 internal/cli/openapi.go                            |  202 ++++
 internal/cli/openapi_contract_test.go              |  411 +++++++
 internal/cli/output.go                             |  116 ++
 internal/cli/output_test.go                        |  246 ++++
 internal/cli/sse.go                                |  151 +++
 internal/cli/sse_test.go                           |  149 +++
 internal/cli/verbs_api.go                          |  315 ++++++
 internal/cli/verbs_api_test.go                     |  820 ++++++++++++++
 internal/cli/verbs_auth.go                         |  289 +++++
 internal/cli/verbs_credential.go                   |  146 +++
 internal/cli/verbs_credential_test.go              |  119 ++
 internal/cli/verbs_datastore.go                    |  209 ++++
 internal/cli/verbs_datastore_test.go               |  215 ++++
 internal/cli/verbs_exec.go                         |  413 +++++++
 internal/cli/verbs_exec_test.go                    |  436 ++++++++
 internal/cli/verbs_node.go                         |  292 +++++
 internal/cli/verbs_node_test.go                    |  196 ++++
 internal/cli/verbs_pack.go                         |  143 +++
 internal/cli/verbs_pack_test.go                    |  195 ++++
 internal/cli/verbs_run.go                          |  257 +++++
 internal/cli/verbs_run_test.go                     |  314 ++++++
 internal/cli/verbs_schedule.go                     |   36 +
 internal/cli/verbs_schedule_test.go                |   46 +
 internal/cli/verbs_system.go                       |  282 +++++
 internal/cli/verbs_system_test.go                  |  182 +++
 internal/cli/verbs_tenant.go                       |   86 ++
 internal/cli/verbs_tenant_test.go                  |  116 ++
 internal/cli/verbs_workflow.go                     |  592 ++++++++++
 internal/cli/verbs_workflow_test.go                |  425 +++++++
 internal/config/config.go                          |  160 ++-
 internal/config/config_test.go                     |   82 ++
 internal/config/embed_branding_test.go             |  192 ++++
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 ++
 internal/config/packs_visibility_test.go           |  168 +++
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/builtin.go                    |   68 ++
 internal/credentials/credentials_test.go           |   58 +
 internal/credentials/registry.go                   |   63 ++
 internal/database/migrate_test.go                  |   17 +
 internal/database/tenant_columns_test.go           |  309 +++++
 internal/database/webhook_route_backfill_test.go   |  491 ++++++++
 internal/datastore/catalogue.go                    |   16 +-
 internal/datastore/column_tenant_test.go           |  152 +++
 internal/datastore/concurrency.go                  |    5 +-
 internal/datastore/doc.go                          |    4 +-
 internal/datastore/engine.go                       |   27 +-
 internal/datastore/engine_test.go                  |   53 +-
 internal/datastore/fleet.go                        |  364 +++++-
 internal/datastore/fleet_engine_test.go            |  711 ++++++++++++
 internal/datastore/isolation.go                    |   96 +-
 internal/datastore/isolation_test.go               |  243 +++-
 internal/datastore/migrate_test.go                 |    1 +
 internal/datastore/model.go                        |    9 +-
 internal/datastore/rows.go                         |   25 +-
 internal/embed/embed.go                            |  121 +-
 internal/embed/embed_branding_test.go              |  164 +++
 internal/embed/embed_lifetime_test.go              |  146 +++
 internal/engine/export_test.go                     |   20 +
 internal/engine/service.go                         |   30 +-
 internal/engine/tenant_visibility_test.go          |  379 +++++++
 internal/engine/wait_service.go                    |   47 +-
 internal/engine/wait_service_test.go               |  219 +++-
 internal/guardrails/compile_scope_test.go          |  440 ++++++++
 internal/idempotency/hash.go                       |   64 ++
 internal/idempotency/hash_test.go                  |  142 +++
 internal/idempotency/idempotency.go                |  432 +++++++
 internal/idempotency/idempotency_test.go           | 1120 +++++++++++++++++++
 internal/idempotency/sweeper.go                    |   94 ++
 internal/idempotency/sweeper_test.go               |  146 +++
 internal/interop/n8n/parameters.go                 |    3 +-
 internal/node/registry.go                          |   31 +
 internal/node/registry_bench_test.go               |  112 ++
 internal/node/visibility.go                        |  347 ++++++
 internal/node/visibility_test.go                   |  796 +++++++++++++
 internal/nodepack/nodepack.go                      |   18 +
 internal/nodepack/trigger.go                       |    8 +
 internal/nodepack/trigger_require_auth_test.go     |   43 +
 internal/nodepack/validate.go                      |   13 +-
 internal/nodepack/visibility_test.go               |  262 +++++
 internal/repository/idempotency.go                 |  360 ++++++
 internal/repository/idempotency_test.go            |  615 ++++++++++
 internal/repository/models.go                      |   23 +-
 internal/repository/postgres_execution_test.go     |   16 +
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_purge.go                |   48 +-
 internal/repository/tenant_purge_test.go           |   48 +
 internal/repository/tenant_rows.go                 |  283 +++++
 internal/repository/tenant_rows_test.go            |  420 +++++++
 internal/repository/webhooks.go                    |  124 +-
 internal/repository/webhooks_delivery_test.go      |  147 +++
 internal/repository/webhooks_test.go               |  184 ++-
 internal/repository/workflows.go                   |   22 +-
 internal/tenantpurge/completeness_test.go          |  368 ++++++
 internal/tenantpurge/doc.go                        |  120 ++
 internal/tenantpurge/docs_test.go                  |  115 ++
 internal/tenantpurge/harness_test.go               |  614 ++++++++++
 internal/tenantpurge/purge.go                      |  412 +++++++
 internal/tenantpurge/purge_test.go                 |  507 +++++++++
 internal/webhook/jwt.go                            |  144 +++
 internal/webhook/jwt_test.go                       |  212 ++++
 internal/webhook/require_auth.go                   |   74 ++
 internal/webhook/require_auth_test.go              |  367 ++++++
 internal/webhook/route_label_test.go               |  172 +++
 internal/webhook/shape.go                          |   27 +-
 internal/webhook/shape_test.go                     |   30 +
 internal/webhook/webhook.go                        |   87 +-
 internal/webhook/webhook_test.go                   |  194 +++-
 internal/workflow/catalog_scope.go                 |   39 +
 internal/workflow/compiler.go                      |   24 +-
 internal/workflow/compiler_visibility_test.go      |  280 +++++
 .../000014_webhook_route_backfill.down.sql         |   14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |   62 +
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../000016_webhook_deliveries_tenant.down.sql      |   14 +
 .../000016_webhook_deliveries_tenant.up.sql        |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   14 +
 .../000017_datastore_columns_tenant.up.sql         |   39 +
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 .../000016_webhook_deliveries_tenant.down.sql      |   13 +
 .../sqlite/000016_webhook_deliveries_tenant.up.sql |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   12 +
 .../sqlite/000017_datastore_columns_tenant.up.sql  |   38 +
 nodes/core.go                                      |    5 +
 nodes/error_workflow.go                            |    4 +
 nodes/http.go                                      |    2 +
 nodes/presentation_test.go                         |   35 +
 nodes/webhook.go                                   |   18 +-
 scripts/check-coordinates.sh                       |   21 +
 scripts/generate-api-reference.mjs                 |   19 +-
 scripts/smoke-cli.sh                               |  228 ++++
 sdk/CHANGELOG.md                                   |   23 +-
 sdk/LICENSE                                        |  202 ++++
 sdk/README.md                                      |   94 +-
 sdk/RELEASING.md                                   |  188 ++++
 sdk/examples/host-page/README.md                   |   64 +-
 sdk/examples/host-page/server.mjs                  |   68 +-
 sdk/package.json                                   |   11 +-
 sdk/pnpm-lock.yaml                                 |    3 +
 sdk/scripts/check-example.mjs                      |  320 ++++++
 sdk/scripts/check-package.mjs                      |  315 ++++++
 sdk/scripts/lib/pack.mjs                           |   77 ++
 sdk/scripts/lib/release.mjs                        |  266 +++++
 sdk/scripts/release.mjs                            |  149 +++
 sdk/src/generated/models.ts                        |  197 +++-
 sdk/src/http.ts                                    |   35 +-
 sdk/src/server.ts                                  |  146 ++-
 sdk/test/operation-coverage.test.mjs               |   23 +
 sdk/test/operations.test.ts                        |   31 +
 sdk/test/release-workflow.test.mjs                 |  223 ++++
 sdk/test/release.test.mjs                          |  390 +++++++
 sdk/test/server.test.ts                            |  108 ++
 web/src/lib/api/generated/admin/admin.ts           |   94 ++
 .../api/generated/datastore-rows/datastore-rows.ts |    4 +-
 web/src/lib/api/generated/models/binaryRemoval.ts  |   16 +
 .../generated/models/executionNodeRunResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |    6 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 .../api/generated/models/tenantDeletionResource.ts |   24 +
 .../models/tenantDeletionResourceRemoved.ts        |   12 +
 web/src/lib/api/generated/nodes/nodes.ts           |    8 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   97 ++
 web/src/lib/dashboard/cursor-page.test.ts          |  285 ++++-
 web/src/lib/dashboard/cursor-page.ts               |   92 +-
 web/src/lib/dashboard/execution-list.test.ts       |  148 ++-
 web/src/lib/dashboard/execution-list.ts            |   98 +-
 web/src/lib/dashboard/workflow-list.test.ts        |   20 +-
 web/src/lib/dashboard/workflow-list.ts             |   21 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |    9 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  143 ++-
 305 files changed, 46282 insertions(+), 1059 deletions(-)
```
