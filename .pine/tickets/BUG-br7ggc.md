---
id: BUG-br7ggc
title: PostgreSQL executions never reach a terminal status
status: todo
priority: critical
labels:
    - platform
    - postgres
created: "2026-09-05T18:01:28Z"
updated: "2026-09-05T18:01:28Z"
---

# Description

Every workflow execution run against the PostgreSQL tier is silently re-executed
for ever. It is not a stuck row — it is an unbounded loop of real work, and it
writes nothing to the log at any severity.

Found while building the Compose quickstart (FEAT-m94hhx), which is what made
the PostgreSQL tier reachable through a documented command for the first time.
The same run succeeds under SQLite, which is why nothing had caught it: no
repository test runs against PostgreSQL at all.

`GORMExecutionStore.UpdateRuntime` is the only path that writes a terminal
status, `finished_at` and `output` onto an `executions` row, and under
PostgreSQL the statement is rejected at parse time. `internal/repository/executions.go:461-470`:

```go
	result := store.db.WithContext(ctx).Model(&executionModel{}).
		Where("tenant_id = ? AND id = ? AND lease_owner = ? AND status IN (?, ?)", …).
		Updates(map[string]any{
			"status": gorm.Expr("CASE WHEN status = ? THEN ? ELSE ? END", …),
			"output": gorm.Expr("CASE WHEN status = ? THEN ? ELSE ? END", …),
			"error":  gorm.Expr("CASE WHEN status = ? THEN ? ELSE ? END", …),
			…
		})
```

`output` and `error` are `bytea` in PostgreSQL
(`migrations/postgres/000001_baseline.up.sql:56-57`) and `blob` in SQLite
(`migrations/sqlite/000001_baseline.up.sql:52-53`). PostgreSQL types a `CASE`
whose branches are all untyped placeholders as `text`, and assignment of a
`text` expression into a `bytea` column is refused — the coerce-via-I/O
fallback applies only when the *target* is a string type:

```
ERROR:  column "data" is of type bytea but expression is of type text
HINT:  You will need to rewrite or cast the expression.
```

Because the error is raised during parse analysis, the whole `UPDATE` is
rejected. `status`, `finished_at`, `lease_owner` and `lease_expires_at` are
never written either. SQLite's type affinity accepts the same statement, which
is the entire difference between the two tiers.

Two things then compound it:

- The error is discarded. `internal/engine/service.go:291-296` runs
  `worked, _ := service.runOnce(ctx, workerID)` — the worker goroutine throws
  the error away and there is no logger call in that loop, so a SQLSTATE 42804
  never reaches stdout.
- Because `worked == true`, the row keeps `status = running` with a live lease
  until `execution.default_timeout` (60s) expires, at which point `ClaimNext`
  reclaims it, deletes its node runs (`internal/repository/executions.go:415`)
  and runs the whole workflow again — failing identically, for ever. Every side
  effect a workflow has is repeated once a minute.

# Steps to Reproduce

```sh
docker compose -f compose.yaml -f compose.postgres.yaml up -d
curl -X POST localhost:8080/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"schemaVersion":1,"name":"x","nodes":[
       {"id":"manual","name":"M","type":"kilasflow.manual","typeVersion":1,"position":{"x":0,"y":0}}],
       "connections":[],"settings":{}}'
curl -X POST localhost:8080/api/v1/workflows/<id>/run -d '{}' -H 'Content-Type: application/json'
curl localhost:8080/api/v1/executions/<execId>
```

# Expected

`"status":"succeeded"`, `finishedAt` set, `output` populated — as the identical
request produces under the SQLite default within milliseconds.

# Actual

`"status":"running"`, `"finishedAt":null`, `"output":null`, indefinitely, while
both entries in `nodeRuns` report `"status":"succeeded"` — node-level writes go
through `tx.Create` with plain `[]byte` fields and no `CASE`, so they persist
correctly. Nothing is logged. Re-verified unchanged after 25 seconds, which was
still inside the first lease; after 60 seconds the workflow silently runs again.

# Acceptance Criteria

- [ ] An execution run against PostgreSQL reaches `succeeded` with `finishedAt`
      and `output` written, matching the SQLite path.
- [ ] The cancellation branch the `CASE` expressions exist to serve still works
      on both drivers — a cancelling execution still lands on `cancelled` with a
      null output and the cancellation error.
- [ ] A driver error inside the worker loop is logged rather than assigned to
      `_`. A failure that repeats a customer's workflow once a minute must not
      be invisible.
- [ ] A repository test covers `UpdateRuntime` against PostgreSQL. There is
      currently no repository test on that driver at all: every
      `GORMExecutionStore` test in `internal/repository/models_test.go` opens
      SQLite, and the only PostgreSQL-aware test file,
      `internal/database/migrate_test.go`, covers migrations and skips unless
      `KILASFLOW_TEST_POSTGRES_DSN` is set. `make smoke-postgres` proves the
      server starts and answers health, not that a workflow completes — worth
      extending, since it is the check that would have caught this.

# Related Files

- `internal/repository/executions.go:445-478` — `UpdateRuntime`, the failing statement.
- `internal/repository/executions.go:365-441` — `ClaimNext`, which reclaims the row and deletes its node runs.
- `internal/engine/service.go:291-296` — the worker loop that discards the error.
- `internal/engine/service.go:153,238,260,642` — the four call sites that funnel into `UpdateRuntime`.
- `migrations/postgres/000001_baseline.up.sql:56-57` and `migrations/sqlite/000001_baseline.up.sql:52-53` — the `bytea`/`blob` split.
- `internal/repository/models.go:229-231` — `Input`/`Output`/`Error` as plain `[]byte`.

Introduced whole in `a51e8dc` ("feat(engine): add deterministic graph execution runtime").
