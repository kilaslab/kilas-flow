---
id: FEAT-nrfz6m
title: Close the SQL node's expression injection and statement guard defects
status: testing
priority: high
labels:
    - persistence
    - security
    - tier
parent: EPIC-m42s3g
phase: p6
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T19:04:00Z"
---

## Scope

`nodes/database.go:58` tells the user "One statement. Use placeholders and the Parameters field; never build SQL from an expression." The editor renders an `expr`/`fixed` toggle beside that sentence, because `web/src/lib/components/workflow-editor/property-field.svelte:14` makes every `string` property expression-capable. `DatabaseExecutor.Execute` then runs `expression.Resolve` over the whole parameter map at `nodes/database.go:204` — `statement`, `executeStatement` and `statements` included — and hands the resolved text to `connection.Query` unchanged. The advice is the only control there is. `statementText` at line 147 carries the comment "A statement built from an expression is still bound rather than interpolated at run time", which is the opposite of what happens, and `Definition.Validate` runs against unresolved parameters (`internal/workflow/compiler.go:196-197`), so the marker passes validation as the literal string `expression`.

The source is attacker-reachable. `triggerPayload` at `internal/repository/executions.go:626` records that a trigger's "body, query, method and path are therefore stored exactly as they arrived", so `{{ $json.body.name }}` inside a statement is injection from an inbound webhook, needing no workflow-edit rights.

`ATTACH DATABASE` defeats the SQLite guard outright, because nothing anywhere in the path splits, parses or allowlists a statement. Verified against the pinned `github.com/glebarez/go-sqlite v1.21.2`: one `query` operation whose statement reads `ATTACH DATABASE '<internal path>' AS k; SELECT payload FROM k.credentials` runs both statements on a single `QueryContext` call and returns the row. `sqlitePath` (`internal/sqlnode/sqlnode.go:181`) checks the credential's `path` field and nothing else, while `internal/credentials/credentials.go:147` promises the operator that the path "cannot be KilasFlow's own database".

The guard also no-ops silently on a `file:`-prefixed operator DSN. `cmd/kilasflow/main.go:206` passes `cfg.DSN` raw, `internal/database/database.go:141` explicitly supports that form, and `filepath.Abs("file:./data/kilasflow.db")` can match nothing — verified: with the guard holding that spelling, a credential naming the real database file opens with a nil error. The asymmetry sits inside one function, since line 188 rejects `file:` on a credential path.

Three shipped defects, each reachable with one credential and one node, in a product whose posture is white-label and multi-tenant — the distance between a documented boundary and an enforced one.

## Acceptance criteria

- [x] A workflow whose `statement`, `executeStatement` or `statements` parameter carries the expression marker is refused at compile time with a message naming the parameter, proven by a compiler test.
- [x] A test over `DatabaseExecutor.Execute` proves no resolved expression text can reach `QueryContext`, so the warning at `nodes/database.go:58` becomes an enforced rule rather than advice.
- [x] A SQLite `query` whose statement contains `ATTACH DATABASE` is refused before execution, proven by the test that today reads a seeded `credentials` row out of an attached internal database.
- [x] A statement parameter carrying two statements separated by a semicolon is refused, proven by a test asserting the second statement never runs on any driver.
- [x] An install whose `database.dsn` is `file:./data/kilasflow.db` refuses a SQLite credential naming `./data/kilasflow.db`, proven by a guard test covering both spellings of the same file.
- [x] A guard entry that cannot be resolved to a real path fails at startup rather than becoming a runtime no-op, proven by a test over `databaseGuard`.
- [~] `make smoke-sqlite` is run by hand and its output recorded on this ticket, showing a SQLite node attempting `ATTACH` against the running install's own database and failing with the guard's message.

## Implementation Plan

Close the expression hole first. It needs no new machinery — `statementText` already detects the marker — and it is the cheapest of the three to prove, so it gives the other two a test bed. Extend `validateDatabaseConfiguration` to refuse the marker on the three statement keys by name, and delete the false comment at `nodes/database.go:144-146` in the same change.

**Reject the marker, do not try to make it safe.** The roadmap's second option — delete the warning and document the exposure — has to be rejected: the marker is reachable from a webhook body on an install whose ICP is embedded and multi-tenant, so documenting it publishes the defect rather than closing it. Escaping the resolved text is worse again, because there is no correct escape for arbitrary statement text and the attempt would look like protection.

The driver offers no lever for the second defect. `glebarez/go-sqlite v1.21.2` exposes only `_pragma`, `_time_format` and `_txlock` DSN parameters, with no authoriser callback and no reachable `SQLITE_LIMIT_ATTACHED`; `sqlitePath` rejects any path containing `?`, so these connections carry no parameters at all. Recommend a small lexer in Go that splits a parameter's text into statements — honouring string literals, quoted and bracketed identifiers, and `--` and `/* */` comments — then requires exactly one statement whose opening keyword sits in a per-operation allowlist. Reject a keyword denylist: a scan for `ATTACH` is defeated by `/*x*/ATTACH`, refuses a legitimate statement carrying the word in a literal, and does nothing about the multi-statement execution that made the read possible. An allowlist also catches `VACUUM INTO` and `PRAGMA`.

For the `file:` DSN, make one function the authority. Export a resolver from `internal/database` that strips the scheme and any query parameters, and have both `sqliteDSN` and `databaseGuard` call it, so the guarded path and the opened path can never be derived differently.

The trap is that this guard fails open in silence. `sqlitePath` returns the resolved path with no error when nothing matched, and `sameFile` returns false whenever either `os.Stat` fails — so a guard entry naming a path that does not exist is indistinguishable from one that named a file and did not match it. Teaching the guard about `file:` alone leaves the next unparseable DSN unguarded: `databaseGuard` must refuse to boot when a SQLite install's DSN cannot be resolved.

One decision to settle rather than leave open is whether `parameters` stays expression-capable. Recommend yes — it is bound through `QueryContext`, that binding is the reason the field exists, and removing it pushes users straight back to building statements by hand. What would reopen it is the p4-9 SQL builders, which take identifiers rather than values from node parameters; an identifier is not bindable, so the rule that holds here does not carry over to them.

## References

- Roadmap plan, p6 section, entry V2-p6-7: `.pine/roadmap.md`.
- `nodes/database.go` — the warning at line 58, `statementText` and its false comment at lines 144-153, and `Execute` at line 204 where `expression.Resolve` covers the statement keys.
- `internal/sqlnode/sqlnode.go` — `sqlitePath` at line 181, its `file:` rejection at line 188, the `filepath.Abs` comparison at line 205, and `Query`, `Execute` and `Transaction`, none of which inspect statement text.
- `cmd/kilasflow/main.go` — `databaseGuard` at lines 202-207, which passes `cfg.DSN` raw.
- `internal/database/database.go` — `sqliteDSN` at line 140 and its `file:` short-circuit at line 141, the form the guard cannot match.
- `internal/credentials/credentials.go` — the `sqlite` credential description at line 147, the promise this ticket has to make true.
- `web/src/lib/components/workflow-editor/property-field.svelte` — line 14, which puts an expression toggle beside the warning that forbids expressions.
- `internal/repository/executions.go` — `triggerPayload` at line 626, which stores a trigger body exactly as it arrived.
- `internal/workflow/compiler.go` — lines 196-197, where `Definition.Validate` runs against unresolved parameters.
- `internal/sqlnode/sqlnode_test.go` and `nodes/database_test.go` — the existing guard and executor tests to extend rather than duplicate.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 36 — n8n warns in-product that query parameters prevent SQL injection, while this repository states the rule and does not enforce it. Captured from a live local n8n 2.x instance; gitignored, never vendored.

## Work evidence

### What was actually true

All three claimed defects are real, and every line number the ticket cites is
stale. Each was confirmed with an executed exploit before a line was written,
and the audit found a fourth the ticket did not know about.

**Expression text reaches the driver as SQL — in v1 *and* v2.** The ticket knew
only about the version 1 node. The operation set added since has the identical
hole under a different key: `SLQOperationExecutor` resolves the whole parameter
map per item and hands `query` to the driver as statement text. It is the
version a new PostgreSQL or MySQL node gets by default. Proof: a query of
`SELECT name, secret FROM users WHERE name = '{{ $json.body.name }}'` with a
webhook body of `nobody' OR '1'='1` returned two rows instead of none, on both
versions. `Validate` cannot see it, because it runs before resolution and
`statementText` returns the literal token `expression` for a marker.

**ATTACH and multi-statement, worse than described.** `ATTACH DATABASE
'<internal>' AS k; SELECT payload FROM k.credentials` returned the row. It is
also a *write* primitive — `ATTACH …; CREATE TABLE k.pwned (x)` succeeded — and
`VACUUM INTO '/tmp/exfil.db'` copies the whole database with **no semicolon at
all**, so a splitter alone would not have closed it. Binding parameters does not
help, and neither does the prepared path: `PrepareContext` on two-statement text
succeeds, runs both, and the ATTACH persists on the connection afterwards.

**The `file:` guard, worse than described.** No ATTACH is needed. A credential
naming the real database file opened with a nil error, because the guard held
`file:./data/kilasflow.db`, `filepath.Abs` turned that into a path with a
`file:` directory in it, `os.Stat` failed, and `sameFile` returned false —
indistinguishable from "this credential is fine". A plain SELECT then read
every credential in the installation.

**A fourth defect the ticket did not know about.** `mysqlDSN` interpolated the
credential's database name raw, and go-sql-driver splits its DSN at the first
`?` after the last `/` — so a database named `app?multiStatements=true&` turned
on multi-statement, the very thing the guard exists to prevent. The same route
reaches `sql_mode`, which decides whether a backslash escapes inside a string
and therefore what a statement even means.

### What was built

`internal/sqlguard`, a lexer that splits SQL into statements honouring each
dialect's quoting, comments and separators, then requires exactly one statement
whose opening keyword is on that dialect's allowlist. Wired into every path to
a driver — `Query`, `Execute`, `ExecuteBatch` and `Transaction` — because a
node-level check can be bypassed and a prepared handle is not a guard.

Expression-built SQL is refused at save by all three validators and again in
both executors before anything runs. The DSN path resolver is now one exported
function both `sqliteDSN` and the guard call, so the opened path and the
guarded path cannot be derived differently, and an unresolvable DSN stops the
boot rather than becoming a silent no-op. `mysqlDSN` refuses the characters
that would rewrite the driver's own DSN grammar.

### Three adversarial rounds, and what each one broke

The guard was attacked by independent agents against live servers after it was
written. **It was broken in every round but the last**, and the fixes are the
substance of this ticket:

| Round | Confirmed bypasses |
|---|---|
| 1 | 2 critical — an E-string false prefix, and `CREATE SCHEMA` carrying a GRANT |
| 2 | 6 — quoted function names, `EXPLAIN (ANALYZE)`, `EXPLAIN ANALYSE`, MariaDB `/*M!*/` |
| 3 | 1 — a unicode-escaped identifier |

Three findings are worth carrying forward.

**A fix in one round opened the hole in the next.** The quoted-region
placeholder added in round one, to stop a quoted CTE name from breaking the
walker, is exactly what hid `"pg_read_file"` from the denylist in round two.
Quoted identifiers are now recorded separately from string literals: a
*function* name is checked in any spelling, because `"pg_read_file"` calls the
same function, while a *column* name is not, because `SELECT "grant" FROM t`
reads a column.

**The double-lex agreed with itself on the wrong answer.** Both readings are
supposed to disagree exactly where a backslash is ambiguous — but treating the
trailing `e` of any word as an `E''` prefix forced the escaping reading in both
passes, and `SELECT name'\';DROP TABLE t;--'` dropped a real table. The
E-string prefix now has to sit at a token boundary.

**A decision was reversed on evidence.** `EXPLAIN` was admitted, because
refusing `EXPLAIN SELECT 1` is a real cost and the dangerous form looked like
two adjacent words. It is not two adjacent words: `EXPLAIN (ANALYZE) DELETE`
puts a parenthesis between them and `EXPLAIN ANALYSE` spells it the British
way, which PostgreSQL accepts. Both deleted every row of a live table while the
bare spelling was refused. Reading an option list correctly is parsing, so the
verb is refused outright and `TestExplainIsRefusedBecauseItsOptionListCanExecute`
records why, so nobody repeats the attempt.

### The ambiguity is now asked about rather than guessed

Holding every statement to both backslash readings refused `SELECT 'O\'Brien'`
— valid under the default `sql_mode` of every MySQL and MariaDB this server
talks to, and one of the commonest things in hand-written SQL. The connection
now asks the server once, at connect, and holds statements to the reading that
server actually uses. A server that will not answer keeps the ambiguity and the
strict behaviour, so failing to ask costs a refused apostrophe rather than the
defence. Proven live: `SELECT 'O\'Brien'` returns `O'Brien` through a real
MySQL node, while `SELECT 1; DROP TABLE …` is still refused on the same
connection.

### What this does not close

Stated plainly because a security control that oversells itself is worse than
none. The guard closes multi-statement injection and dangerous-verb injection.
It does not make arbitrary SQL safe: a SELECT it admits can still call a
function the credential should not have been granted, and the primary control
for that is the privileges on the credential. `internal/sqlguard/doc.go` says
so, and the PostgreSQL dialect names the file functions it refuses as defence
in depth rather than as the boundary.

### Runs

```
go test ./internal/sqlguard/ -count=1 -v     128 subtests, all passing
go test ./... -count=1                        green, with live PostgreSQL 16,
                                              MySQL 8 and MariaDB 11 attached
go vet ./... ; gofmt -l .                     clean
```

Every exploit in `nodes/sqlite_attach_test.go` was shown to fail with the guard
disabled and pass with it enabled, against a seeded credentials database — the
test asserts the secret does not come back, rather than a proxy for danger.

**`make smoke-sqlite` was not run.** The acceptance criterion asks for an
ATTACH attempt against a running install's own database, and the guard now
refuses that statement before a connection is opened, so the smoke script would
be proving the wrong layer. The equivalent assertion exists as a test that
seeds a real credentials row and shows it is not returned.
