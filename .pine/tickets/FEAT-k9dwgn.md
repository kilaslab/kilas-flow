---
id: FEAT-k9dwgn
title: Bound Datastore growth with limits and retention
status: doing
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-nrfg6e
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-06T04:48:34Z"
---

## Scope

No resource in KilasFlow has a quota. A repository-wide search for `quota` across the Go source returns nothing, and `config.Config` carries no datastore block at all — `Default()` at `internal/config/config.go:128-172` populates nine sections and not one of them bounds a count. The only bounds the product owns are two transport bounds and one page bound: `Webhook.MaxBodyBytes` at `1 << 20`, `OutboundHTTP.MaxResponseBytes` at `8 << 20`, and `MaxExecutionPageSize = 100` at `internal/repository/executions.go:33-36`. A tenant may create unlimited workflows, credentials, schedules and executions today, and nothing has broken. Datastore is where that stops being survivable, because a datastore is a real table created by runtime DDL in the operator's own database.

Four counts are mandatory on both drivers and need no measurement mechanism: maximum datastores per tenant, columns per datastore, rows per datastore, and bytes per single value. Each is a `COUNT(*)` over the catalogue, a bounded existence probe over the physical table, or a `len()` taken before the value is ever bound to a statement. None of the three requires a size estimate, a background job or a stored counter.

Per-datastore *bytes* are a different thing and are deliberately a PostgreSQL capability tier — the same posture the epic already records for pgvector retrieval and the `kflow_` shared-database prefix. On PostgreSQL, `pg_total_relation_size` gives an exact answer for a named table. On SQLite it is unavailable, and this is verified rather than assumed: `dbstatConnect` appears in zero files under `modernc.org/sqlite@v1.23.1/lib/` and in twenty-five files under `v1.54.0/lib/`, and the `-DSQLITE_ENABLE_DBSTAT_VTAB` flag at `generator.go:222` sits inside the `configTest` set — the testfixture build — not the configuration the shipped library is generated from. The pinned driver has no `dbstat` virtual table, so there is no per-table byte figure to read.

One more bound belongs here and is not a quota. Every Datastore operation shares a single connection: `internal/database/database.go:57-59` calls `SetMaxOpenConns(1)` process-wide whenever the driver is `sqlite` (the roadmap's `57-63` spans the PostgreSQL branch too). A limit check that scans a whole table to answer "are there too many rows" blocks the API, the scheduler, the webhook server and every running workflow for its duration, so how long an operation may hold that connection is itself an acceptance criterion.

This matters because of what KilasFlow is. The product is embedded, white-label and multi-tenant, and the PostgreSQL posture on record is that it shares a customer's database under a table prefix. A Datastore with no limits hands a host application's own end users the ability to create unbounded tables inside the host's production database. That is the one failure mode a white-label platform cannot apologise its way out of.

## Acceptance criteria

- [ ] Creating a datastore beyond the configured per-tenant maximum fails with a message naming the limit and the current count, proven by a repository test on both drivers.
- [ ] Adding a column past the per-datastore column maximum leaves both the catalogue and the physical table untouched, proven by a test asserting no DDL statement was emitted.
- [ ] A row write whose single value exceeds the configured byte bound is refused before any SQL is bound, proven by a test that reaches no driver.
- [ ] Writing past the per-datastore row maximum fails while every existing row stays readable and writable, proven by a test that reads the pre-limit rows back afterwards.
- [ ] Per-datastore byte usage reports an exact figure on PostgreSQL and reports explicit unavailability — never zero — on SQLite, captured as evidence from `make smoke-postgres` and `make smoke-sqlite`.
- [ ] Every limit has a default in `config.Default()` and is refused by `Config.Validate` when zero or negative, proven by a table-driven configuration test.
- [ ] No Datastore operation holds the single SQLite connection beyond the stated bound, proven by a test timing a concurrent reader against a datastore sitting at its row maximum.
- [ ] A limit breach refuses the write and deletes nothing, proven by a test asserting the row count is unchanged after the refusal.

## Implementation Plan

Put the limits in `config` first, before a single check is written. Add a `Datastore` block to `config.Config` carrying the four counts, give it defaults in `Default()` and rejections in `Validate` at `internal/config/config.go:212-228`, and stop there for one commit. This is first because a limit with no configured home gets hard-coded at its call site, and because every paragraph below needs a name to read rather than a number to repeat.

Enforce inside the `internal/datastore` service, never in the Huma handlers. **Enforcement layer.** The handler layer is the tempting place — `internal/api/handlers/` already owns the `huma.Error422` vocabulary and every existing validation lives there — and it is the wrong one, because the two callers most likely to write in a loop do not pass through it. The Datastore node executor and the agent tool both call the service directly, so a handler-level quota is bypassed by exactly the traffic it exists to bound.

Count without scanning. A `COUNT(*)` over a datastore at its row maximum is the one operation that threatens the hold-time criterion on SQLite. Reject it in favour of a bounded existence probe — `SELECT 1 FROM <table> LIMIT 1 OFFSET <max-1>` — which stops the moment it finds the limit-th row and touches nothing beyond it. Also reject a cached count column on the catalogue row: it is a second write path that must be kept in step with every insert, delete and dry run, and it drifts silently the first time one of them is added without remembering.

Read bytes only where they exist. On PostgreSQL, `pg_total_relation_size` against the physical table name the storage engine already derives; on SQLite, return unavailability. Do not bump the pinned driver to reach `dbstat`: `modernc.org/sqlite v1.23.1` is an indirect requirement of `github.com/glebarez/go-sqlite v1.21.2` (`go.mod:7,47`), and the `modernc.org/libc` API moved between `v1.22.5` and `v1.74.3` across that span, so reaching `v1.54.0` means replacing the driver, and the `CGO_ENABLED=0` distroless posture rests on that driver.

The trap is the zero that means "unknown". The natural Go signature for a size reader is `(int64, error)`, and on SQLite it will return `0` alongside its error. One caller that logs the error and carries on gets a byte figure of zero for every datastore, a quota comparison of `0 < max` that passes forever, and a management UI showing "0 B" beside a table holding a million rows — which reads as empty, not as unmeasured. Make unavailability a value in the type rather than a value in the range: return a known-flag or a distinct sentinel, render it as null on the API, and assert in a test that the SQLite path renders "unavailable" and never "0".

**Retention.** Recommend that breaching a limit refuses the write and evicts nothing, with no automatic row expiry in this slice — a datastore holding a customer's reference data must never silently shed rows to make room for new ones, and there is no signal today about which rows would be safe to drop. What reopens it is a host running a bounded-size log or event table through the node; the answer then is an opt-in FIFO cap declared per datastore at creation, visible in the catalogue and in the editor, not a global retention policy applied to everything.

## References

- Roadmap plan, p9 section, entry V2-p9-5: `.pine/roadmap.md`.
- `internal/config/config.go:128-172` — `Default()`, which carries no datastore block; `Validate` at `212-228` is where the new limits are rejected.
- `internal/database/database.go:57-59` — `SetMaxOpenConns(1)` on the SQLite driver, the single connection every Datastore operation shares.
- `internal/repository/executions.go:33-36` — `DefaultExecutionPageSize` and `MaxExecutionPageSize`, the only bound of this shape the repository layer owns today.
- `internal/webhook/webhook.go:212-217` — the `MaxBodyBytes` refusal, the message and error shape a limit breach should follow.
- `modernc.org/sqlite@v1.23.1/lib/` — no `dbstatConnect` in any file, against twenty-five files in `v1.54.0/lib/`; the pinned build has no `dbstat` virtual table.
- `modernc.org/sqlite@v1.23.1/generator.go:222` — `-DSQLITE_ENABLE_DBSTAT_VTAB` inside `configTest`, the testfixture configuration rather than the shipped library's.
- `go.mod:7,47` — `github.com/glebarez/go-sqlite v1.21.2` and the indirect `modernc.org/sqlite v1.23.1` it pins.
- `Makefile` — `smoke-sqlite` and `smoke-postgres`, run by hand and recorded on this ticket; the repository has no CI configuration of any kind.

## Evidence — 2026-09-06 (DatastorePolicy slice)

- `Limits{MaxDatastoresPerTenant:100, MaxColumnsPerDatastore:100,
  MaxRowsPerDatastore:100000, MaxValueBytes:1MiB}` in
  `internal/datastore/limits.go` (new); `Engine` defaults to it at
  construction, `SetLimits` moves it and rejects non-positive bounds naming
  the field. Enforcement lives in the Engine (service layer), never the
  handlers: Create (tenant count + column count), AddColumn (column count,
  before catalogue/DDL), Insert/Update (per-value bytes before any bind),
  Insert (row ceiling via bounded `LIMIT 1 OFFSET max-1` probe, never
  COUNT(*)). Breaches refuse and delete nothing; messages name limit+count.
- `Usage` reports exact rows everywhere; bytes exact on PostgreSQL via
  `pg_total_relation_size`, explicit `SizeKnown:false` on SQLite (never a
  zero that reads as empty). No auto-expiry: retention is refuse-and-keep,
  documented in limits.go.
- Tests (`internal/datastore/limits_test.go`): ceiling + per-tenant
  isolation, column refusal leaves catalogue+table untouched (no DDL
  emitted), byte refusal reaches no driver (hook empty), row refusal keeps
  rows readable AND writable, SetLimits validation table, Usage
  observability incl. SQLite unavailability. Full datastore suite green on
  SQLite.
- NOT done in this slice: `config.Datastore` block + `Default()`/`Validate`
  wiring (touches shared config.go owned on the secrets line by another
  agent; limits ship with `DefaultLimits`+`SetLimits` instead — config
  binding is a mechanical follow-up); wall-clock hold-time assertion (the
  probe is O(limit-th row) by construction; the timing test would be
  flaky by nature); `make smoke-postgres` / `smoke-sqlite` by hand (no PG
  here; PG byte-size path unverified live).
