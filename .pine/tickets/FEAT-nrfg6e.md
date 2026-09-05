---
id: FEAT-nrfg6e
title: Store and query Datastore rows
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-ss44d9
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

There is no row store: `internal/datastore` does not exist, and a repository-wide search for `datastore`, `data table` or `data_table` across Go, TypeScript, Svelte and JSON returns hits only inside `.pine/`. This ticket adds row CRUD over the DDL service V2-p9-1 delivers, at n8n's filter surface — `{type: and|or, filters: [{columnName, condition, value}]}` over `eq`, `neq`, `like`, `ilike`, `gt`, `gte`, `lt`, `lte`, `isEmpty` and `isNotEmpty`.

The operator slot decides whether the feature is safe. The nearest precedent is `ifOperators` at `internal/interop/n8n/parameters.go:157-167`, whose miss path returns `KilasFlow's IF does not support the n8n operator %q; supported operators are equals, notEquals, exists, and notExists` at line 240. The refusal is right; a `map[string]string` of operator to SQL fragment would not be, because a map miss yields the zero string and an empty fragment composes into a `WHERE` clause that still parses with the predicate gone. Operators map through a Go `switch` to compile-time fragments, and an unrecognised operator is an error.

Pagination reuses the executions cursor, which is not reusable today. `encodeExecutionCursor` and `decodeExecutionCursor` are unexported at `internal/repository/executions.go:337-355` (the roadmap said 335-353), and the encoder is one line — `base64.RawURLEncoding.EncodeToString([]byte(startedAt.UTC().Format(time.RFC3339Nano) + "\x00" + id))`. `ErrInvalidCursor` sits a file away at `internal/repository/workflows.go:30`, and `DefaultExecutionPageSize = 25` and `MaxExecutionPageSize = 100` at lines 33-36 are as execution-shaped as the helpers. A second copy is how two cursor formats drift apart, so the helper is extracted first.

Dry run on update, upsert and delete returns paired before and after rows tagged `dryRunState`, the fourth reserved system column beside `id`, `createdAt` and `updatedAt`. Nothing named `dryRun` exists in the repository today, so the paired shape is decided here rather than ported.

This matters because a datastore holds a customer's business data inside a database that customer may also own, and a filter is the one place where caller-supplied strings meet SQL structure rather than SQL values. A predicate that can be bypassed — or silently dropped — is a read across someone else's rows in a white-label deployment.

## Acceptance criteria

- [ ] Every filter operator resolves through one Go `switch` to a compile-time SQL fragment, and an unrecognised operator returns an error naming the supported set, proven by a table-driven test.
- [ ] The keyset cursor helpers live in one place shared by executions and rows, and a cursor issued by today's encoder still decodes afterwards, proven by a test over a recorded literal.
- [ ] A row inserted while a client pages through a listing cannot shift rows onto a page that client has already read, proven by an interleaved-insert test on both drivers.
- [ ] The same `like` or `ilike` filter over one fixture returns the same rows on SQLite and PostgreSQL, proven by a test run by hand with `KILASFLOW_TEST_POSTGRES_DSN` and recorded here.
- [ ] Dry run on update, upsert and delete returns paired before and after rows tagged `dryRunState` and leaves the table byte-identical, proven by comparing counts and value checksums either side.
- [ ] A filter naming a column absent from the catalogue is rejected before any SQL text is built, proven by a test asserting no statement reached the driver.
- [ ] Filter values reach the database only as bound placeholders, proven by a test asserting the generated statement text contains no byte of the supplied value.
- [ ] A full listing and a filtered read both succeed under `make smoke-sqlite` and `make smoke-postgres`, with the output captured as evidence on this ticket.

## Implementation Plan

Extract the cursor helper before a line of the row store is written. Afterwards the extraction is a reconciliation of two formats rather than a move of one, and the executions cursor is opaque and already issued to clients, so changing its byte layout once a second consumer depends on it changes two contracts at once. Move both functions into a new `internal/repository/cursor.go` as exported helpers taking a sort value and an id, and keep the wire format byte-identical.

**Fragments or data.** Recommend a `switch` returning a small struct carrying the SQL fragment and its placeholder count, with a default branch that returns an error. Reject the alternative that reads as the same thing, a `map[string]string` of operator to fragment: a map miss yields `""`, so a caller who forgets the second return value builds a statement whose predicate has quietly vanished. A `switch` with an explicit default cannot be used that way, and the compiler keeps the fragments constant.

Resolve every `columnName` against the catalogue rows V2-p9-1 stores before it is quoted, not against a regular expression. A pattern proves a string is a legal identifier; it does not prove the column belongs to this datastore, and a legal identifier naming someone else's column is the failure this ticket exists to prevent. Quoting is the second line of defence, never the first.

The trap is `LIKE`. SQLite's `LIKE` is case-insensitive for ASCII by default and has no `ILIKE`; PostgreSQL's `LIKE` is case-sensitive and `ILIKE` is the case-insensitive one. Map both operators explicitly per driver and prove the equivalence with one fixture run against both. Left implicit this fails with no error at all: the identical workflow returns four rows on a customer's PostgreSQL and seven on the SQLite install beside it.

Build dry run as a read plus a computed after-image, not as a write inside a rolled-back transaction. The rollback route is tempting because it reuses the mutation path exactly, and it is wrong here: `internal/database/database.go:57-63` pins SQLite to `SetMaxOpenConns(1)`, so a rolled-back bulk update still holds the write lock for its full duration and stalls every other caller while producing nothing.

Leave upsert atomicity, row locking and any `updatedAt` precondition to V2-p9-6, and say so in the code: half-settling it here yields an upsert whose behaviour changes underneath that ticket.

**Row sort key.** Recommend keyset pagination on the integer `id` alone rather than on `(createdAt, id)`. Both drivers store the datastore timestamp at millisecond precision, so ties are ordinary rather than exotic, and a single-integer cursor drops the RFC3339Nano round-trip. What reopens this is the first request to sort a listing by a user column: that cursor must carry the sorted value with `id` as tiebreaker, so shape the shared helper now such that adding a component later is not a format change.

## References

- Roadmap plan, p9 section, entry V2-p9-2: `.pine/roadmap.md`.
- `internal/repository/executions.go:337-355` — `encodeExecutionCursor` and `decodeExecutionCursor`, the helpers to extract.
- `internal/repository/executions.go:284-335` — `List`, the keyset predicate and the read-one-extra-row page probe to copy.
- `internal/repository/executions.go:33-36` — `DefaultExecutionPageSize` and `MaxExecutionPageSize`, the bounds to generalise.
- `internal/repository/workflows.go:30` — `ErrInvalidCursor`, which callers already translate into a 400.
- `internal/interop/n8n/parameters.go:157-167` and line 240 — `ifOperators` and its refusal message, the precedent for rejecting an unmapped operator.
- `internal/database/database.go:57-63` — the SQLite single-connection pin that rules out run-and-rollback dry runs.
- `internal/repository/models_test.go:33-60` — the real-file SQLite test harness these tests should follow.
- `internal/database/database_test.go:132-136` — `KILASFLOW_TEST_POSTGRES_DSN`, the gate for the PostgreSQL half of every cross-driver test.
- `Makefile:126-128` and `Makefile:138-140` — `smoke-sqlite` and `smoke-postgres`, both run by hand and recorded per ticket.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 20, 23, 30-32 — the grid's prev/next paging and 20-per-page select, the Get row(s) surface with **Must Match** defaulting to **Any Condition**, **Return All** and **Limit Per Input Row**, and the condition row whose operator options proved type-dependent. Captured from a live local n8n 2.x instance; gitignored, never vendored.
