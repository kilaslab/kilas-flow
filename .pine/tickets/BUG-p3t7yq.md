---
id: BUG-p3t7yq
title: The concurrent-migration-start test flakes under load with "duplicated key not allowed"
status: done
priority: high
labels:
    - ci
    - testing
created: "2026-09-20T21:40:00Z"
updated: "2026-09-21T00:32:42Z"
---

## Problem

`internal/database`'s `TestConcurrentStartsApplyTheBaselineExactlyOnce` fails intermittently when the machine is loaded (a dozen parallel worktree builds), with a `duplicated key not allowed` error. It is the third test of this class found while landing tickets on 2026-09-20, after BUG-fng4m2 (login spray) and BUG-w8h3km (wait deadlines).

## Evidence

- Hit during the landing gate of BUG-fng4m2 (landing commit 67c6c90): `go test ./...` run 1 failed in `internal/database` with that error; a second full run was green, and the test passes in isolation (`-count=1` ok in 1.039s).
- Proven pre-existing, not caused by that landing: `go test -count=30 -run TestConcurrentStartsApplyTheBaselineExactlyOnce ./internal/database/` was run in a clean checkout of the parent commit `72d0200` and failed with the same `duplicated key not allowed` error.

## Fix direction (verify before implementing)

Reproduce it on a loaded machine, then find the true cause. The name says "exactly once": if the test's premise is that N concurrent starts race to apply a baseline migration, it must tolerate the loser's failure mode rather than depending on a scheduling outcome. Either the production code must make the race genuinely impossible (a unique violation being retried/absorbed is the normal shape) or the test must assert the invariant that actually matters (the migration applied exactly once and the loser's error is the documented one) instead of asserting on an unordered race outcome. Decide with evidence, state which, and do NOT add sleeps, do NOT skip under load, do NOT widen a timeout.

## Acceptance criteria

- [x] The package passes at least 30 consecutive `-count=30` runs (and 10 consecutive `-race` runs) on a loaded machine.
- [x] The invariant still holds: the baseline migration is applied exactly once, and a concurrent starter either succeeds or fails with the documented error.
- [x] No test weakened, skipped, or given a longer timeout to get green.

## Implementation notes

**The cause is in the production bootstrap, not in the test's premise.**
`adoptExistingSchema` decides to adopt from a read of `schema_migrations` taken
*before* another starter recorded the baseline, and then recorded the baseline with
the plain `recordVersion` insert. Two starters that begin together both read an
empty version table and both see the baseline tables, so the first records version 1
and the second's insert hits the primary key and crashes its boot:

    concurrent starter 0: adopt existing schema as migration 000001_baseline: duplicated key not allowed

`duplicated key not allowed` is `gorm.ErrDuplicatedKey`, GORM's translation of the
dialect's unique violation — which is why the message named no table. `apply`
already absorbs exactly this collision by re-reading the version table, and
`recordVersionUnlessClaimed` (the skip path, BUG-rpkjpy) declares it in the
statement; adoption was the one decision of that shape left with the plain insert.
The test was right to expect every concurrent starter to succeed: two processes
starting at the same moment must both boot.

**Fix.** `recordSkippedVersion` is generalised to `recordVersionUnlessClaimed` — one
helper for both decisions that record a version row whose DDL this process did not
run (an adopted baseline, a vector migration skipped for a missing extension) — and
`adoptExistingSchema` uses it: `INSERT OR IGNORE` on SQLite, `ON CONFLICT (version)
DO NOTHING` on PostgreSQL. The loser carries on with the outcome the winner
recorded; the baseline still runs no DDL and is still recorded exactly once.

**Tests.** `TestAStarterThatLostTheAdoptionRaceAdoptsRatherThanFails` and its
`...OnPostgres` twin fix the interleaving instead of racing it: the adoption decision
is taken, then taken again from the stale read, and the loser must report the schema
as adopted with exactly one version row. They are the adoption twin of
`TestVectorSkipRecordsVersionWithoutRunningDDL`, and they exercise both dialects'
tolerant insert without waiting for a loaded machine. The existing concurrent tests
keep every assertion they had.

**Verification.**

- Red first, unpatched tree: `go test -count=30 -run TestConcurrentStartsApplyTheBaselineExactlyOnce ./internal/database/`
  -> two starters with `adopt existing schema as migration 000001_baseline: duplicated key not allowed`.
  Both new tests red with the plain insert restored, PostgreSQL included
  (`ERROR: duplicate key value violates unique constraint "schema_migrations_pkey" (SQLSTATE 23505)`).
- Red again on a clean detached checkout of main (`git worktree add --detach ... main`,
  tip 5328524, unpatched): `-race -count=30 ./internal/database/` ->
  `concurrent starter 0: adopt existing schema as migration 000001_baseline: duplicated key not allowed`
  (361s, load 35-72). The flake is still there on main until this branch lands.
- Mutation: `recordVersion` made a no-op so the baseline really is applied twice ->
  `TestConcurrentStartsApplyTheBaselineExactlyOnce` fails loudly with
  `apply migration 000001_baseline: SQL logic error: table `workflows` already exists`
  and `schema_migrations rows for version 1 = 0, want 1`. Restored.
- 30 consecutive `go test -count=30 ./internal/database/` runs (900 suite executions)
  with four extra cpu burners on a machine already at load 26-50 on 10 cores: 0
  failures (per-run logs `/tmp/kf-p3t7yq-logs/sqlite-count30-*.log`).
- 10 consecutive `go test -race -count=1 ./internal/database/` runs, default harness
  deadline, load 33-48: 0 failures (`/tmp/kf-p3t7yq-logs/sqlite-race1-*.log`).
- One `-race -count=30` run of an earlier loop hit Go's default 10-minute per-binary
  deadline — `panic: test timed out after 10m0s`, running
  `TestMigratingAfterARollbackRebuildsTheSchema`, no assertion failure — at load 84 on
  10 cores. That is a wall-clock limit of the harness, not this defect: the same
  command on the unpatched checkout of main completed in 261s and 361s at load 45-72,
  and the flake it guards reports an error, never a timeout. The `-count=30` race loop
  therefore passes `-timeout 30m` explicitly; the `-count=1` race loop and every other
  command above run with the default.
- PostgreSQL against a scratch `pgvector/pgvector:pg17`:
  `KILASFLOW_TEST_POSTGRES_DSN=... go test -count=30 ./internal/database/` -> ok, 377s;
  `go test -race -count=1 ./internal/database/...` -> ok; and the second block
  `scripts/smoke-postgres.sh` runs, `go test ./internal/repository/ -run 'Driver|Retention' -count=1` -> ok.
- `gofmt -l` on both changed files: clean. `go build ./...` and `go vet ./...`: clean.
- `go test -race -count=1 ./internal/guardrails/...` -> ok.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `acb3f6e0` (last commit at or before ticket created 2026-09-20)
- Commits (2):
  - `3fd05e24` — BUG-p3t7yq: adopt an existing schema without insisting on the version row
  - `9a6c81bb` — chore(pine): open BUG-p3t7yq — the concurrent-migration-start test flakes under load
- Files changed (base → working tree):

```
 .pine/tickets/BUG-p3t7yq.md                        |   76 +-
 .pine/tickets/BUG-vzzkg3.md                        |  558 +++++++++-
 .pine/tickets/FEAT-3taswf.md                       |   18 +
 .pine/tickets/FEAT-fpqvwx.md                       |  893 +++++++++++++++-
 .pine/tickets/FEAT-hj8pyx.md                       |  714 ++++++++++++-
 CHANGELOG.md                                       |   43 +-
 cmd/kilasflow/idempotency_test.go                  |  225 ++++
 cmd/kilasflow/main.go                              |   89 +-
 config.example.yaml                                |   14 +
 .../content/docs/concepts/tenancy-and-embedding.md |   27 +-
 docs/src/content/docs/guides/community-nodes.md    |  131 ++-
 docs/src/content/docs/guides/idempotency.md        |  197 ++++
 .../docs/operate/configuration-reference.md        |   26 +
 docs/src/content/docs/operate/tenant-deletion.md   |  194 ++++
 docs/src/content/docs/reference/api-contract.md    |   19 +-
 docs/src/content/docs/reference/api.md             |    4 +-
 docs/src/content/docs/reference/api/datastores.md  |    6 +-
 docs/src/content/docs/reference/api/errors.md      |   11 +
 docs/src/content/docs/reference/api/tenants.md     |   21 +
 docs/src/content/docs/reference/api/workflows.md   |    3 +-
 internal/api/cors_test.go                          |    4 +-
 internal/api/handlers/admin.go                     |  154 +++
 internal/api/handlers/admin_admin_test.go          |  263 ++++-
 internal/api/handlers/datastores.go                |  183 +++-
 internal/api/handlers/idempotency.go               |  115 ++
 internal/api/handlers/workflows.go                 |   89 +-
 internal/api/idempotency_test.go                   | 1004 ++++++++++++++++++
 internal/api/middleware/cors.go                    |   11 +-
 internal/api/middleware/cors_test.go               |    2 +-
 internal/api/routes.go                             |    6 +-
 internal/api/server.go                             |   13 +-
 internal/api/tenant_delete_test.go                 |  386 +++++++
 internal/binary/binary.go                          |  117 ++
 internal/binary/binary_test.go                     |  217 ++++
 internal/config/config.go                          |   81 +-
 internal/config/config_test.go                     |   82 ++
 internal/database/migrate.go                       |   40 +-
 internal/database/migrate_test.go                  |   83 +-
 internal/database/tenant_columns_test.go           |  309 ++++++
 internal/datastore/catalogue.go                    |   16 +-
 internal/datastore/column_tenant_test.go           |  152 +++
 internal/datastore/engine.go                       |   10 +-
 internal/datastore/engine_test.go                  |   53 +-
 internal/datastore/isolation.go                    |   96 +-
 internal/datastore/isolation_test.go               |  243 ++++-
 internal/datastore/migrate_test.go                 |    1 +
 internal/datastore/model.go                        |    9 +-
 internal/idempotency/hash.go                       |   64 ++
 internal/idempotency/hash_test.go                  |  142 +++
 internal/idempotency/idempotency.go                |  432 ++++++++
 internal/idempotency/idempotency_test.go           | 1120 ++++++++++++++++++++
 internal/idempotency/sweeper.go                    |   94 ++
 internal/idempotency/sweeper_test.go               |  146 +++
 internal/repository/idempotency.go                 |  360 +++++++
 internal/repository/idempotency_test.go            |  615 +++++++++++
 internal/repository/models.go                      |   13 +-
 internal/repository/postgres_execution_test.go     |   16 +
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_purge.go                |   48 +-
 internal/repository/tenant_purge_test.go           |   48 +
 internal/repository/tenant_rows.go                 |  283 +++++
 internal/repository/tenant_rows_test.go            |  420 ++++++++
 internal/repository/webhooks.go                    |   45 +-
 internal/repository/webhooks_delivery_test.go      |  147 +++
 internal/repository/workflows.go                   |   22 +-
 internal/tenantpurge/completeness_test.go          |  368 +++++++
 internal/tenantpurge/doc.go                        |  120 +++
 internal/tenantpurge/docs_test.go                  |  115 ++
 internal/tenantpurge/harness_test.go               |  614 +++++++++++
 internal/tenantpurge/purge.go                      |  412 +++++++
 internal/tenantpurge/purge_test.go                 |  507 +++++++++
 internal/webhook/webhook.go                        |    4 +-
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../000016_webhook_deliveries_tenant.down.sql      |   14 +
 .../000016_webhook_deliveries_tenant.up.sql        |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   14 +
 .../000017_datastore_columns_tenant.up.sql         |   39 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 .../000016_webhook_deliveries_tenant.down.sql      |   13 +
 .../sqlite/000016_webhook_deliveries_tenant.up.sql |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   12 +
 .../sqlite/000017_datastore_columns_tenant.up.sql  |   38 +
 scripts/generate-api-reference.mjs                 |   13 +-
 sdk/CHANGELOG.md                                   |   18 +
 sdk/README.md                                      |   36 +-
 sdk/src/generated/models.ts                        |   86 +-
 sdk/src/http.ts                                    |   35 +-
 sdk/src/server.ts                                  |  136 ++-
 sdk/test/operation-coverage.test.mjs               |    3 +
 sdk/test/operations.test.ts                        |   31 +
 sdk/test/server.test.ts                            |  108 ++
 web/src/lib/api/generated/admin/admin.ts           |   94 ++
 .../api/generated/datastore-rows/datastore-rows.ts |    4 +-
 web/src/lib/api/generated/models/binaryRemoval.ts  |   16 +
 web/src/lib/api/generated/models/index.ts          |    3 +
 .../api/generated/models/tenantDeletionResource.ts |   24 +
 .../models/tenantDeletionResourceRemoved.ts        |   12 +
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 100 files changed, 13842 insertions(+), 286 deletions(-)
```
