---
id: BUG-p3t7yq
title: The concurrent-migration-start test flakes under load with "duplicated key not allowed"
status: doing
priority: high
labels:
    - ci
    - testing
created: "2026-09-20T21:40:00Z"
updated: "2026-09-20T21:40:00Z"
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


