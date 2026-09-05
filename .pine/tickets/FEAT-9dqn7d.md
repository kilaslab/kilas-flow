---
id: FEAT-9dqn7d
title: Close the five real gaps in the SQL node
status: todo
priority: high
labels:
    - nodes
    - parity
    - sql
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T08:28:30Z"
---

## Scope

`DatabaseExecutor.Execute` calls `runOne` once per input item (`nodes/database.go:203-213`), so a hundred-row insert is a hundred statements, and `sqlnode.Open` pins the pool with `db.SetMaxOpenConns(1)` (`internal/sqlnode/sqlnode.go:100`; the roadmap's 99-101 spans the comment above it), so they cannot even overlap. The loop is why there are N round trips; the pin is why they are serial.

`Connection.Transaction` runs every statement through `tx.ExecContext` (`internal/sqlnode/sqlnode.go:368`, not 367 as the roadmap has it) and returns `Rows: []map[string]any{}` for each, so a transaction can never hand back a row: `INSERT … RETURNING id` commits and the generated id is discarded, leaving only the summary item's `rowsAffected`, `statements` and `"committed": true` (`nodes/database.go:264-268`).

The `parameters` property carries no `VisibleWhen` (`nodes/database.go:71-74`) while the three statement properties each carry one, and the editor filters on that field alone (`web/src/lib/components/workflow-editor/properties-panel.svelte:36`), so Parameters is shown for every operation. `runOne` decodes it at `nodes/database.go:222` but uses `bound` only in the query and execute branches, so a malformed array fails a transaction that would have ignored it.

`maxRows` and `timeoutSeconds` come from the per-item resolved parameters with no server-side ceiling (`nodes/database.go:218-221`), and `Limits.MaxRows` is the only bound on the row buffer. The roadmap's `{{ 500000000 }}` is not the vector — `parse` refuses a body that does not start with `$` (`internal/expression/expression.go:217-219`) — but `{{ $json.maxRows }}` over a webhook body is, since a whole-expression marker returns the looked-up value with its own type.

`timeoutSeconds` is declared twice: "Statement timeout (seconds)" at `Default: 30` in Parameters (`nodes/database.go:75`) and "Timeout (seconds)" at `Default: 0` in `sharedSettings` (`nodes/core.go:132`; the roadmap says 124). `validateProperties` cannot see the collision, because `validateDefinition` calls it once per group with a fresh `seen` map (`internal/node/registry.go:220`, `:223`, `:241`), and at run time the two land in different stores — one bounds a statement, the other the whole node through `nodeContext` in `internal/engine/runner.go`, which reads `node.Settings["timeoutSeconds"]` — while the form shows two boxes that disagree.

Bulk write is the commonest thing asked of a database node, and this is the family where a customer's own data lives. Each of the five is met in the first hour of use, and none of them needs a new property kind, a new endpoint or a version bump.

## Acceptance criteria

- [ ] An execute over 500 input items runs on one prepared statement inside one transaction, proven by a test that fails a middle statement and finds nothing committed.
- [ ] The per-item output of an execute is unchanged — one `rowsAffected` item per input item — pinned by a test so existing downstream nodes see the same stream.
- [ ] A transaction statement declared as returning rows yields those rows as items, so `INSERT … RETURNING id` reaches the next node, proven by a SQLite test.
- [ ] Setting `parameters` on a transaction is refused at validation with a message naming the per-statement field, rather than silently ignored, proven by a validator test.
- [ ] `maxRows` and `timeoutSeconds` arriving from an expression are clamped to the configured ceiling and the clamp is marked on the output item, proven by a test driving both from `$json`.
- [ ] `validateProperties` rejects any definition declaring one key in both Parameters and SharedSettings, and the database and HTTP nodes both pass, proven by `go test ./...`.
- [ ] The PostgreSQL half of the batching and returning-rows coverage is run by hand with `KILASFLOW_TEST_POSTGRES_DSN` set and its output recorded on this ticket.

## Implementation Plan

Start with the duplicated `timeoutSeconds`, because it is the only one of the five whose fix belongs in `internal/node/registry.go` rather than the database family, and because the cross-group check fails on `nodes/http.go:85` as well. That answer decides whether this ticket also carries a second node's rename, and it costs one test run now against a rework halfway through the batching change.

For bulk writes, prepare once and reuse: one transaction around the item loop, `tx.PrepareContext` on the execute statement, each item's bound parameters through the prepared handle. The pinned single connection is an asset here, since a prepared statement stays on the connection it was built for. Reject raising `SetMaxOpenConns` and fanning items out concurrently — it multiplies open connections against a customer's database, exactly the posture the package comment at `internal/sqlnode/sqlnode.go:1-7` protects, and it makes an ordered insert non-deterministic. Reject n8n's `queryBatching` names too: those belong to V2-p4-11, which depends on V2-p4-9's operation set.

For rows out of a transaction, extend the decoded statement shape in `transactionStatements` (`nodes/database.go:297-320`) with an explicit per-statement flag and route a flagged statement through `tx.QueryContext`. Reject sniffing the SQL text for `RETURNING` or a leading `SELECT`: comments, CTEs and `WITH … RETURNING` all defeat it, and the answer would differ per driver.

The trap is that a blanket switch to `QueryContext` looks correct and is not. A non-returning statement run through Query comes back as a rows set with zero columns and no reachable `RowsAffected`, so the summary item keeps saying `"committed": true` while `rowsAffected` silently becomes zero for every transaction in the installation. Nothing errors, a test that only checks the commit passes, and the first report is a user reconciling counts weeks later.

Put the ceiling in configuration under a one-word koanf section: `envKeyToPath` cuts an environment key at its first underscore (`internal/config/config.go:241-250`), so a two-word section could never be overridden — the same reason `OutboundHTTP` is `outbound`. Thread it through `RegisterExecutors` (`nodes/executors.go:23`) beside the guard, both being deployment decisions rather than document ones, and clamp loudly rather than refusing the run, marking the item as the query path already does with `$truncated` (`nodes/database.go:240`).

**Visibility gate.** `parameters` cannot be shown for query and execute but hidden for transaction, because `VisibilityCondition` is single-key equality AND-ed together (`internal/node/registry.go:32-35`) and the editor implements exactly that. Recommend the validator rejection now, leaving the field visible, rather than forking `queryParameters` and `executeParameters`, which changes every stored document and the interop mapping for a cosmetic gain. This reopens if V2-p2-3's array-valued conditions slip past this phase, when the forked pair becomes the cheaper of two bad options.

## References

- Roadmap plan, p4 section, entry V2-p4-6: `.pine/roadmap.md`.
- `nodes/database.go` — the per-item loop, the ungated `parameters` property, the unbounded limits, and the first `timeoutSeconds`.
- `internal/sqlnode/sqlnode.go` — `Open`'s pool pin and `Transaction`'s exclusive use of `tx.ExecContext`.
- `nodes/core.go` — `sharedSettings`, which declares the second `timeoutSeconds`.
- `internal/node/registry.go` — `validateDefinition` and `validateProperties`, whose `seen` map is per group.
- `internal/engine/runner.go` — `nodeContext`, which reads the shared setting from `node.Settings`.
- `internal/expression/expression.go` — `parse` and `lookup`, which bound what an expression can supply to a numeric parameter.
- `web/src/lib/components/workflow-editor/properties-panel.svelte` — the only consumer of `visibleWhen`.
- `internal/config/config.go` — `OutboundHTTP` and `envKeyToPath`, the pattern a limits section has to follow.
- `.pine/tickets/FEAT-vwzd6r.md` — the ticket that shipped the database family, for the decisions this one must not undo.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 36 — n8n's own in-product warning on the Query field: *"Consider using query parameters to prevent SQL injection attacks. Add them in the options below"*. Compare `nodes/database.go`, which states the same rule in a property description and then resolves expressions over the statement anyway. Captured from a live local n8n 2.x instance; gitignored, never vendored.
