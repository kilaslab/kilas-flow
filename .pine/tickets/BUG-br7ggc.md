---
id: BUG-br7ggc
title: PostgreSQL executions never reach a terminal status
status: testing
priority: critical
labels:
    - platform
    - postgres
created: "2026-09-05T18:01:28Z"
updated: "2026-09-05T19:09:32Z"
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

- [x] An execution run against PostgreSQL reaches `succeeded` with `finishedAt`
      and `output` written, matching the SQLite path.
- [x] The cancellation branch the `CASE` expressions exist to serve still works
      on both drivers — a cancelling execution still lands on `cancelled` with a
      null output and the cancellation error.
- [x] A driver error inside the worker loop is logged rather than assigned to
      `_`. A failure that repeats a customer's workflow once a minute must not
      be invisible.
- [x] A repository test covers `UpdateRuntime` against PostgreSQL. There is
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

## Work evidence

Every claim in the report was confirmed against a live PostgreSQL 16 before
anything was changed, and the diagnosis was exactly right.

### The fix

The `CASE` is gone, replaced by two updates inside one transaction — one for a
cancelling execution, one for a running one. Exactly one matches, because a row
is in one status or the other, and the transaction stops it changing between
them. That is dialect-neutral: each placeholder lands directly in the column it
belongs to, so the driver types it from that column instead of PostgreSQL
typing a `CASE` of untyped placeholders as `text` and refusing to assign it
into `bytea`.

Rejected: casting the branches. `CAST(? AS bytea)` fixes PostgreSQL and breaks
SQLite, which has no such type, and putting dialect-specific SQL in the
repository would mean the two tiers stop being the same code — which is the
condition that produced this defect in the first place.

### The silence

`Service` had no logger at all. It has one now, defaulting to `slog.Default()`
rather than refusing to start, and `cmd/kilasflow` passes the real one. The
worker's `worked, _ := service.runOnce(…)` is now `worked, err :=` with the
error logged. It is deliberately not fatal — one execution failing to record
its outcome must not stop the others — but a driver error that repeats a
customer's workflow once a minute cannot be invisible.

### The test that would have caught it

There was no repository test against PostgreSQL at all: every
`GORMExecutionStore` test opened SQLite, and the only PostgreSQL-aware file
covered migrations. `internal/repository/postgres_execution_test.go` adds an
`eachDriver` helper that runs a case against SQLite and, when
`KILASFLOW_TEST_POSTGRES_DSN` is set, against a live server — because a test
that only ever sees one driver cannot see a difference between two.

Two cases: an execution reaches a terminal status with `finishedAt`, `output`
and a released lease; and a cancelling execution still lands on `cancelled`
even when the worker reports success, which is the race the `CASE` existed to
win and which the replacement must not lose.

**Proven to catch the defect.** With the `CASE` restored, the PostgreSQL half
fails with the exact reported error:

```
--- FAIL: TestAnExecutionReachesATerminalStatusOnEveryDriver/postgres
    UpdateRuntime() error = update execution runtime state:
    ERROR: column "error" is of type bytea but expression is of type text (SQLSTATE 42804)
```

and the SQLite half passes either way — which is the whole reason this shipped.

### Runs

```
go test ./internal/repository/ -run "TestAnExecutionReaches|TestACancelling" -count=1 -v
  sqlite    PASS      postgres  PASS   (both cases, both drivers)
go test ./... -count=1     green, with live PostgreSQL 16, MySQL 8 and MariaDB 11
go vet ./... ; gofmt -l .  clean
```

The suggestion to extend `make smoke-postgres` to prove a workflow *completes*
rather than only that the server answers health is not done here, and is worth
its own ticket: it is a change to the smoke script's shape rather than to this
defect, and the repository test now covers the same ground closer to the code.
