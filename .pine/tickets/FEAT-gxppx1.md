---
id: FEAT-gxppx1
title: Migrate per-Datastore tables across schema versions
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-gvn62x
    - FEAT-ss44d9
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

`database.Migrate` at `internal/database/database.go:106-116` is still one `db.AutoMigrate(models...)` call at line 111, and V2-p6-1 (`FEAT-gvn62x`) replaces it with goose over numbered `.sql` files under `migrations/` — a directory that today holds nothing but `.gitkeep`. Neither the reflection it removes nor the file runner it installs can express the change this phase makes inevitable: "for each of N tenant-created tables discovered at run time, alter the shape". That second engine is this ticket.

Goose cannot be stretched to cover it, for a structural reason rather than a missing feature. Its unit of work is a numbered file applied once per database and recorded in one version table; the datastore tables are an unbounded set, enumerated from the catalogue rather than from the repository, created after the process started, and free to sit at different versions from one another. A `.sql` file cannot iterate a table list on either driver, and the version table exists precisely to stop the re-running that iteration needs.

The runner therefore needs four properties, none of which anything in the repository has today. A schema version per datastore, readable without opening that datastore's table. Iteration that is idempotent and resumable, because the process will be killed halfway at some point. A defined state when it fails with datastores at mixed versions. And a bound on how long a step may hold the SQLite connection, which `internal/database/database.go:57-63` pins to `SetMaxOpenConns(1)` for the whole process.

Half-migrated is currently invisible. `Ready` at `internal/api/handlers/system.go:83-94` does one thing, `s.db.Ping(ctx)`, and `ReadyOutput` at lines 41-48 carries `Status`, `Database` and `Error` and no other field, so an instance with half its datastores at version three reports `ok`. Placement is unsolved too: `cmd/kilasflow/main.go:79` calls `migrate(db)` before the HTTP server is constructed.

This ticket exists so the cost is scheduled rather than discovered. The first datastore row-shape change lands on installs that already hold live tenant data, in a database the customer may also own, and the moment it is needed is the worst possible moment to be designing a runner.

## Acceptance criteria

- [ ] Every datastore carries its own schema version, readable without opening its physical table, and a datastore created while the runner is working is born at the current version, proven by a concurrent-create test.
- [ ] Running the runner twice over the same fleet applies each step exactly once, proven by a test that counts step invocations rather than inspecting the resulting schema.
- [ ] Killing the process mid-run leaves every datastore fully at the old version or fully at the new one, never between, proven by a test that interrupts between and inside steps.
- [ ] A resumed run visits only datastores still behind, proven by a test asserting the already-migrated set is never opened again.
- [ ] No single step holds the SQLite connection longer than a configured bound; exceeding it aborts that step and leaves its datastore at the previous version, with the timing captured as evidence here.
- [ ] Readiness reports the fleet's version spread, so a half-migrated instance is visibly half-migrated instead of green, proven by a handler test over a mixed-version fixture.
- [ ] A binary meeting a datastore at a version it does not know refuses that datastore's operations with an error naming both versions and keeps serving every other datastore, proven by a test.
- [ ] A fleet migration across a multi-datastore install completes under `make smoke-sqlite` and `make smoke-postgres`, with the run summary recorded here.

## Implementation Plan

Decide where the per-datastore version lives before writing the runner, because the work-list query, the resume path and the create path all read it. Recommend an integer `schema_version` column on the `datastores` catalogue row V2-p9-1 introduces. Reject putting the version inside the physical table — a marker column, or SQLite's `PRAGMA user_version`: `user_version` does not exist on PostgreSQL, and either form forces one query per table to build a work list, a full fleet scan before any work begins.

Re-evaluate the work list on every iteration rather than snapshotting it once. Select one datastore behind the current version, claim it, migrate it, stamp it, repeat. A snapshot taken at the start is the version that misses datastores created during the run, and the roadmap's "unbounded set" is a moving set, not merely a large one.

Claim with a lease rather than a lock. `ClaimNext` at `internal/repository/executions.go:360-436` establishes the shape — an owner and an expiry on the row, reclaimable once the expiry passes — so a runner killed mid-step releases its claim by timeout instead of leaving one datastore unreachable. Reject a PostgreSQL advisory lock as the mechanism: it does not exist on SQLite, and advisory locking is V2-p6-4's subject.

Put each datastore's DDL, its backfill and its version stamp in one transaction. DDL is transactional on both drivers, so this is what makes the all-or-nothing criterion true rather than aspirational, and it is cheaper than logic that tries to undo a half-applied step afterwards.

The trap is `IF NOT EXISTS`. A step written as `ADD COLUMN IF NOT EXISTS` looks idempotent and is not: after a crash between the backfill and the stamp, the re-run finds the column present, quietly skips the DDL, and runs the backfill a second time — doubling counters, re-appending defaults, overwriting values a user has since edited. Nothing errors and nothing logs. Idempotence comes from the stamp sharing a transaction with the work, never from a clause that lets a repeated statement silently succeed.

Bound the SQLite hold by chunking the work, not by cancelling the context: a deadline cannot un-hold a connection already inside one long statement. A backfill runs in bounded batches with a row limit per transaction, and the runner checks its budget between them.

Run the fleet after the server is serving, not inside `migrate(db)` at `cmd/kilasflow/main.go:79`. Startup there predates the HTTP server, so a fleet pass in that position turns a tenant's datastore count into the instance's boot time; the per-datastore refusal covers the window while the runner catches up.

**Whether the row store serves two versions at once.** Recommend no: a datastore behind the binary refuses operations with a named error until the runner reaches it, rather than the row store carrying a branch per version. What reopens this is a step slow enough that refusing is worse than serving the old shape — at which point the change splits into an expand phase both versions can serve and a later contract phase.

## References

- Roadmap plan, p9 section, entry V2-p9-4: `.pine/roadmap.md`.
- `internal/database/database.go:106-116` — `Migrate` and its single `AutoMigrate` call, the engine V2-p6-1 replaces.
- `internal/database/database.go:57-63` — the SQLite single-connection pin that every bound in this ticket exists to respect.
- `.pine/tickets/FEAT-gvn62x.md` — V2-p6-1, the goose runner over static numbered files this one sits beside.
- `migrations/` — today holds only `.gitkeep`; the static set lands there and the datastore steps deliberately do not.
- `internal/repository/executions.go:360-436` — `ClaimNext`, the existing lease-and-reclaim pattern to reuse.
- `internal/api/handlers/system.go:41-48` and `:83-94` — `ReadyOutput` and `Ready`, which report database reachability and nothing about schema state.
- `cmd/kilasflow/main.go:79` and `:193-195` — the `migrate(db)` call site and helper, and the reason the fleet pass belongs after it.
- `.pine/tickets/EPIC-m42s3g.md:31` — the decisions-table entry sanctioning runtime DDL for datastores, which is what makes a second engine necessary.
- `Makefile:126-128` and `Makefile:138-140` — `smoke-sqlite` and `smoke-postgres`, both run by hand and recorded per ticket.
