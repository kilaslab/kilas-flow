---
id: BUG-s0wy50
title: PostgreSQL without pgvector cannot boot (migration 000006 unconditional)
status: doing
priority: high
labels:
    - postgres
    - migrations
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T14:24:35Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 2 finding(s) from dims: find:build-health-dx.

---
### PostgreSQL without pgvector cannot boot: migration 000006 unconditionally runs CREATE EXTENSION vector (regression of FEAT-k65hqv plan/AC); CI Go tests red with 110 failures [find:build-health-dx] (high/bug) · area: database/migrations/deployment · confidence: high

The vector-store migration makes the pgvector extension (and CREATE EXTENSION privilege) a hard boot requirement for the whole PostgreSQL tier. A plain PostgreSQL, a managed PG without the extension, or the documented shared-customer-database topology with a restricted role gets half-migrated (v5) and then refuses to start. That contradicts the ticket that introduced it.

Evidence: migrations/postgres/000006_vector_store.up.sql: first statement `CREATE EXTENSION IF NOT EXISTS vector;`. Live test: I started my own postgres:17-alpine on 127.0.0.1:55433, then ran `KILASFLOW_DATABASE_DRIVER=postgres KILASFLOW_DATABASE_DSN=postgres://...:55433/kf kilasflow`. Migrations 1-5 applied, then exit 1 with `kilasflow: apply migration 000006_vector_store: ERROR: extension "vector" is not available (SQLSTATE 0A000)` (scratchpad work/build-health-dx/pgplain/boot.log). CI run 34571965928 (main, 2026-09-11), job "Go tests", uses the postgres:17-alpine service and shows 110 identical failures across internal/database, internal/datastore, internal/engine and internal/repository. FEAT-k65hqv (done) plan says: "a migration that fails on it takes the whole install down at boot for a feature nobody asked for. Recommend an explicit opt-in database.vector flag". Its AC says that on a Postgr

Impact: Any PostgreSQL deployment outside the bundled pgvector/pgvector:pg17 compose overlay fails to start, including the white-label shared-DB topology. The failure leaves a half-migrated schema. CI on main is red because of it.

Suggested fix: Gate the vector migrations behind an opt-in (database.vector) or make 000006 conditional: probe for the extension, skip it and use DisabledVectorStore with a boot WARN when it is unavailable. Switch the CI Postgres service to pgvector/pgvector:pg17 and add a plain-PG boot smoke test. Document the requirement and the privilege needed.

Files: migrations/postgres/000006_vector_store.up.sql, internal/database/migrate.go, .github/workflows/ci.yml, docs/src/content/docs/operate/deployment.md

Existing tickets: FEAT-k65hqv

---
### CI is red on main: live SQL tests use a zero-policy guard that the database SSRF policy refuses, plus pgvector and client-drift failures; newest run cancelled [find:build-health-dx] (high/dx) · area: CI / Go tests · confidence: high

The last completed CI run on main failed in "Go tests" (115 FAILs) and "Generated client drift". The newest run (HEAD bf802ab) was cancelled. Besides the pgvector problem, the V2 SQL live tests build `sqlnode.Guard{}` with no allowance. Since the database network policy landed on 2026-09-06, that guard refuses every loopback CI database, so these tests fail on every driver whenever the DSN env vars are set.

Evidence: `gh run list`: 2026-09-11 run 34571965928 = failure (jobs "Go tests" and "Generated client drift"); 2026-09-15 run = cancelled. Failure log: nodes/sql_options_live_test.go:94,184,235,274,340,407, apostrophe_live_test.go:27 and internal/sqlbuild/sqlbuild_test.go:531 all fail with `database target is not allowed: postgres|mysql host "127.0.0.1" resolved to 127.0.0.1: request target is not allowed: loopback address`. The cause is newV2Executor at sql_options_live_test.go:493-499 using sqlnode.Guard{}; the guard policy arrived in eaf914e (FEAT-4d0bje). I reproduced the 6 postgres failures deterministically, also with -p 1, against my own pgvector container with KILASFLOW_TEST_POSTGRES_DSN. Also in the failure log: internal/datastore rows_test.go:327 "drivers disagree: sqlite [[2] [1 2 3]] postgres [[] []]". The plain `go test ./... -p 4 -count=1` run without DSNs passes: 44 ok, 57 env-gated 

Impact: Regressions on the PostgreSQL, MySQL and MariaDB paths are invisible. A red main has gone unnoticed for 8 days.

Suggested fix: Give the live tests a Guard with Policy.AllowedPrivateEndpoints set to the DSN's host:port, as database_test.go:804 and pgvector_test.go:172 already do. Fix the Postgres service image (see the pgvector finding) and regenerate the web client. Make the CI jobs required checks with a visible status on main.

Files: nodes/sql_options_live_test.go, nodes/apostrophe_live_test.go, internal/sqlbuild/sqlbuild_test.go, .github/workflows/ci.yml

Existing tickets: FEAT-4d0bje

## Acceptance criteria

- [ ] PostgreSQL without pgvector cannot boot: migration 000006 unconditionally runs CREATE EXTENSION vector (regres
- [ ] CI is red on main: live SQL tests use a zero-policy guard that the database SSRF policy refuses, plus pgvector
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress 2026-09-19 (SecurityDx)
- Landed f79cb9a: migrateFS skips CREATE-EXTENSION-vector migrations on PG without pgvector (statement-detected, version recorded as applied, WARN) + needsVectorExtension/vectorAvailable helpers + live-test zero-Guard fixes (nodes/sql_options_live_test.go liveGuard, internal/sqlbuild openLive). Scoped: go test ./internal/database/ -run 'TestVector|TestAFailedMigration|TestEveryMigration' PASS; ./internal/sqlbuild/ PASS.
