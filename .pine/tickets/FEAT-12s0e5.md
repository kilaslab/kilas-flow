---
id: FEAT-12s0e5
title: Bring the MySQL node to n8n's operation set
status: done
priority: high
labels:
    - nodes
    - parity
    - sql
deps:
    - FEAT-n5fdz3
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T15:09:36Z"
---

## Scope

`databaseNode()` at `nodes/database.go:38` is one factory producing three definitions that differ in exactly four strings — a node type, an executor id, a display name and a credential type. `postgresNode()`, `mysqlNode()` and `sqliteNode()` (lines 84 to 94) each register `Version: workflow.V(1)` with the same three operations, `query`, `execute` and `transaction`. There is no dialect anywhere in the family: not in the definition, not in `DatabaseExecutor`, and not in `internal/sqlnode`, whose only per-driver code is two DSN builders and the SQLite path guard.

MySQL needs one the moment V2-p4-9's operation set exists, because those operations emit SQL rather than accept it. Identifiers are backtick-quoted, and an embedded backtick is escaped by doubling it. Placeholders are positional `?`, not `$1`. And there is no schema qualifier: MySQL's schema is its database, which is why the `mysql` credential at `internal/credentials/credentials.go:133-144` carries `host`, `port`, `database`, `user`, `password` and `tls` and nothing more, while `postgres` at 121 to 132 carries `sslMode` and would need a schema on top.

The option collection differs too — `connectionLimit`, `decimalNumbers`, `priority`, `selectDistinct` and `detailedOutput` have no Postgres counterpart, and their exact names and defaults must be read out of the reference checkout rather than transcribed. `connectionLimit` contradicts `sqlnode.Open` outright: `db.SetMaxOpenConns(1)` at `internal/sqlnode/sqlnode.go:100` pins every node run to one connection, deliberately.

The version story is smaller than the roadmap implies. n8n serves MySQL at `defaultVersion` 2.5 and Postgres at 2.7 — the numbers V2-p4-12 raises the export pins to — but there is no `defaultVersion` field to add here. `Registry.Resolve` at `internal/node/registry.go:131` already returns the highest registered version at or below the one requested, and the highest of all when a document requests none.

That leaves `kilasflow.sqlite`, which has no parity target because n8n ships no SQLite node, and is nonetheless not idle: `nodes/database_test.go` drives the whole family through `nodes.SQLiteNodeType`, transaction rollback and row truncation included. Whatever is done to the shared factory is done to the substrate the family's tests run on.

This matters because the customers this platform is aimed at run MySQL and MariaDB at least as often as PostgreSQL. A parity programme that reaches six operations on one dialect and leaves the other on hand-written SQL imports half the SQL nodes it advertises, and the half it misses fails at run time rather than at import.

## Acceptance criteria

- [x] `kilasflow.mysql` registers the n8n operation set as a second type version while a document asking for version 1 still resolves to the query/execute/transaction shape, proven by a registry test.
- [x] Every identifier a MySQL builder emits is backtick-quoted with embedded backticks doubled, proven by a fuzz test over adversarial table and column names asserting the emitted text.
- [x] No MySQL builder output contains a `$1`-style placeholder or a qualified `schema.table` name, proven by a test that asserts the SQL text rather than the query result.
- [ ] ~~The MySQL option collection carries n8n's own key set and defaults~~ — **not done, deliberately.** There is no options collection on either database node yet; FEAT-g6wrxm adds the mechanism. Building a MySQL-only one here would mean building it twice and reconciling two shapes later. What this ticket did instead was read n8n's MySQL collection out of the widened checkout and record what it holds, so g6wrxm starts from the transcription rather than the guess.
- [ ] ~~`connectionLimit` either changes the pool or is refused~~ — same reason. The reference settles one open question for g6wrxm: **`connectionLimit` is a node option in n8n's MySQL collection, not a credential field**, so the refusal it needs is a node-level diagnostic naming `SetMaxOpenConns(1)`.
- [x] `kilasflow.sqlite` keeps one registered version and exposes no operation string it has no implementation for, proven by a registry test over the whole database family.
- [x] The existing `nodes/database_test.go` suite passes unchanged against SQLite, showing the dialect extraction altered no behaviour the family already had.
- [x] The MySQL integration run against a disposable MariaDB **and** MySQL 8 is executed by hand and its output recorded below, because this repository has no CI of any kind.

## Outcome

### What the ticket asked for that had already happened

Its central instruction — extract a dialect from `databaseNode()`'s four unrelated strings — is obsolete. The PostgreSQL operation set landed as a standalone `postgresV2Node()` and never touched `databaseNode()`, which is frozen at v1. **The dialect that needed extracting was in `internal/sqlbuild`, not in the node factory**, and refactoring the frozen v1 family to express a difference it does not have would have been churn with the whole family's test substrate at risk.

Its claim that no dialect exists anywhere is also stale: `Connection.placeholder`, the MySQL branches in `Schemas` and `Columns`, and the SQLite refusal all landed with schema introspection. That was the precedent this followed rather than a greenfield decision.

### One dialect value, not two packages

`sqlbuild.Dialect` is a struct with two package-level instances, passed as each builder's first argument.

Not an interface, because this is the one code path where user input reaches statement text and an interface makes the quoting rule something a caller outside the package can supply — the same argument the closed comparison set already makes. Not two packages either: that copies the builders' *shared* shape — the sorted-key determinism, the refusal of a delete with no condition, the empty-SET refusal — so a later fix to one lands in one copy and the two golden directories then diverge for a reason that is **not** a dialect difference, which is exactly what a golden file exists to make visible.

**The migration proved itself.** `git mv` the goldens into `testdata/postgres/`, thread the dialect through, run the suite *without* `-update-golden`: all nine PostgreSQL statements came back byte-identical, which is the whole proof the refactor changed nothing. Two packages could not have offered that.

### Where MySQL diverges, and what each divergence cost

- **Backticks, doubled**, hand-written rather than pgx's — and correct under both `sql_mode` settings, where a double quote is only an identifier quote when `ANSI_QUOTES` is set.
- **64 characters, not 63 bytes**, and the two limits differ *in kind*: PostgreSQL truncates silently so its refusal prevents two names colliding into one, while MySQL rejects an over-long name itself so its refusal only buys a better message. A 22-character CJK name is 66 bytes — accepted by one, refused by the other, and a test pins exactly that.
- **A trailing space is refused**, because MySQL rejects it in a table name even when quoted.
- **Never qualified.** `` `a`.`b` `` on MySQL names *database* a's table b, so a qualified target would silently address the wrong database on every statement the node builds. A live single-database fixture cannot catch this — against a fixture where `a` *is* the database it runs and passes — so it is asserted on the SQL text.
- **`ON DUPLICATE KEY UPDATE c = VALUES(c)`**, not MySQL 8.0.19's `AS new … new.c` row alias. The alias is unavailable on MySQL 5.7 and on MariaDB entirely, and this credential is documented as serving both — so the alias would break every MariaDB user to silence a deprecation warning. Version sniffing was rejected: a round trip per run and a branch only one server ever exercises.
- **`LOWER(…) LIKE LOWER(…)` for `ilike`**, because MySQL has none. It defeats an index on that column, which is the cost of the operator meaning what it says under a `_bin` or `_cs` collation rather than relying on whichever the table happens to carry.
- **No `RETURNING`**, which is the decision below.

### What an insert returns without RETURNING

Two convenient answers, both rejected:

**Echoing back the values the node wrote** hides column defaults, the auto-increment key, `ON UPDATE CURRENT_TIMESTAMP`, generated columns, triggers, and MySQL's own coercion — a `VARCHAR(10)` given twenty characters comes back as twenty in the echo and is ten in the table.

**`SELECT LAST_INSERT_ID()`** is connection-scoped and **keeps its previous value when a statement generates no key**. The executor runs every item's statement on one pinned connection, so an insert into a table without an auto-increment column would report the *previous item's* id. Plausible, wrong, and silent.

The honest answer is the driver's own `LastInsertId()` for *that statement*, read off the OK packet at no extra round trip: zero rather than stale where none was made. A live test proves exactly that — an auto-increment insert reports a key, and the very next insert into a table without one reports **0**.

MariaDB's `INSERT … RETURNING` was rejected for the same reason as the row alias: using it splits the dialect on a runtime version probe.

### The version flip, worse here than for PostgreSQL

Same guard, sharper problem. Every already-imported MySQL node is sitting in storage as `{"operation": "query"}` **with no statement at all**, because the old importer flattened every operation but `executeQuery` and dropped the SQL with it. Those documents now fail validation loudly, at save and activation, with a message naming both remedies — rather than failing at run time on an unknown operation, or running and doing nothing.

`sqlToKilas` and `sqlToN8N` were the last users of the old flattening and are deleted rather than left unreachable.

### The reference checkout

Widened to carry `packages/nodes-base/nodes/MySql` and `Postgres`, which it did not before — the ticket's closing sentence was a live prerequisite and is now discharged. It confirmed the six operation values are **the same as PostgreSQL's**, so the constants are shared rather than declared twice; that the default is `insert` where PostgreSQL's is `executeQuery`; and that there is no schema field. It stays read-only specification material and no test reads it.

### The evidence

```
$ KILASFLOW_TEST_MYSQL_DSN=... KILASFLOW_TEST_MARIADB_DSN=...     go test ./internal/sqlbuild -run TestMySQLIdentifiersAndUpserts -v
--- PASS: TestMySQLIdentifiersAndUpsertsAgainstALiveServer (0.41s)
    --- PASS: .../mysql (0.34s)
    --- PASS: .../mariadb (0.07s)

$ go test ./internal/sqlbuild -run FuzzIdentifier -fuzz FuzzIdentifier -fuzztime=40s
fuzz: elapsed: 41s, execs: 420690, new interesting: 72 (total: 142)
PASS
```

MySQL 8.4.11 and MariaDB 11.8.9. Both servers, because they differ on precisely the two decisions this ticket makes. The fuzz target loops both dialects rather than splitting in two, which keeps the committed corpus live; it now holds 123 entries. `KILASFLOW_TEST_MARIADB_DSN` is new and recorded in `.pine/memory/live-databases.md`.

Live upsert numbers, asserted rather than assumed: **1** for an insert, **2** for an update, **0** when the row existed and nothing changed. Passing that through as a row count would mislead, which is why the node names it separately.

### The corpus

Unchanged: 13/39 activatable, 3/39 runnable, identical diagnostic counts. The MySQL nodes in the corpus sit in workflows blocked on credentials. What changed is that an imported MySQL insert is now an insert rather than an empty query.

## Implementation Plan

Extract the dialect before writing a single operation. Every later decision here — quoting, placeholders, qualifier policy, the option collection, the version to register — hangs off which database is being addressed, and `databaseNode()` currently encodes that as four unrelated strings passed by three call sites. Turning those four into one dialect value is what makes the rest expressible, and it is a pure refactor with an existing suite standing behind it.

Recommend a `dialect` value carrying a quoting function, a placeholder function, a qualifier policy and the option collection, passed once into both the definition factory and the SQL builders. Reject a `switch driver` inside each builder: the Postgres and MySQL difference would then live in a dozen places, and the next dialect — or the Datastore DDL in p9 — would have to find all of them. One value, constructed once, is also the only shape a fuzz test can be written against.

The trap is the schema qualifier. MySQL reads `` `a`.`b` `` as *database* dot table, not schema dot table, so a shared builder that emits a qualified name for MySQL does not fail — it silently addresses a different database on the same server, one the credential very often can reach. Nothing errors, the row count is plausible, and the wrong table is read or written. Hence the criterion asserting emitted SQL text: a result-based test against a single-database fixture cannot see this defect at all.

`?` binds need no such guard. `go-sql-driver/mysql` rejects `$1` outright, so a placeholder mistake is loud on the first run; the qualifier is the only dialect difference that fails quietly.

Leave the DSN alone. `mysqlDSN` at `internal/sqlnode/sqlnode.go:150` writes `?parseTime=true` plus an optional `tls`, and neither `multiStatements` nor `interpolateParams` belongs in this ticket — batching is V2-p4-11's, and both flags change the binding contract the package comment at `internal/sqlnode/sqlnode.go:1-7` exists to hold. Note too that MySQL's `LAST_INSERT_ID()` and `ROW_COUNT()` are connection-scoped and are correct today only because the pool is pinned at line 100; an operation that reads them must record that dependency where the pool is configured, not where the SQL is built.

**The SQLite fork.** Recommend keeping `kilasflow.sqlite` on the shared factory at version 1 and never registering a version 2 for it. There is no parity target, no corpus fixture and no import path that produces one, so a locally invented SQLite operation set would be six SQL builders nothing measures — while the node's real job, being the family's test substrate, wants it identical to what the other two were before the split. Reject a hand-written `nodes/database/sqlite.go` definition: it duplicates three properties and a validator to express a difference that does not exist. What would reopen it is the p9 Datastore work choosing SQLite as a first-class runtime-DDL target, at which point SQLite acquires a parity target of its own and the fork earns its cost.

## References

- Roadmap plan, p4 section, entry V2-p4-10: `.pine/roadmap.md`.
- `nodes/database.go` — `databaseNode` at line 38, the three constructors at 84 to 94, and `DatabaseCredentialType`.
- `internal/sqlnode/sqlnode.go` — the package comment's binding contract, `mysqlDSN` at line 150, `dataSource` at 112, and the pinned pool at line 100.
- `internal/credentials/credentials.go` — the `mysql` fields at 133 to 144, which carry no schema because MySQL has none, against `postgres` at 121 to 132.
- `internal/node/registry.go` — `Resolve` at line 131, which is what this codebase has instead of n8n's `defaultVersion`.
- `nodes/database_test.go` — the whole family's tests, every one of which opens a SQLite file through `nodes.SQLiteNodeType`.
- n8n 2.34.0 reference checkout (read-only, outside this repo): `packages/nodes-base/nodes/MySql/` for the option collection and the node's `defaultVersion`. The checkout is sparse and currently holds only `HttpRequest`, `If`, `Schedule` and `Set`, and V2-p0-1's widening list does not add the database nodes, so `packages/nodes-base/nodes/{Postgres,MySql}` must join the sparse-checkout before this ticket starts.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `4816f2ed` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `cf72f8d0` — chore(pine): open phase p9 for the Datastore and route database parity to p4
- Files changed (base → working tree):

```
 .gitignore                                         |    3 +
 .pine/MEMORY.md                                    |    2 +
 .pine/memory/code-node.md                          |   41 +
 .pine/memory/live-databases.md                     |   40 +
 .pine/roadmap.md                                   |  257 ++
 .pine/tickets/EPIC-m42s3g.md                       |   10 +-
 .pine/tickets/FEAT-0556ck.md                       |   66 +
 .pine/tickets/FEAT-0f87fn.md                       |  300 +-
 .pine/tickets/FEAT-12s0e5.md                       |  134 +
 .pine/tickets/FEAT-1axhdn.md                       |   65 +
 .pine/tickets/FEAT-1c70nt.md                       |   73 +
 .pine/tickets/FEAT-27km39.md                       |   71 +
 .pine/tickets/FEAT-2f68r8.md                       |   81 +-
 .pine/tickets/FEAT-2phs15.md                       |  461 +++
 .pine/tickets/FEAT-3taswf.md                       |   67 +
 .pine/tickets/FEAT-3xqky1.md                       |   70 +
 .pine/tickets/FEAT-45tfmh.md                       |  438 +++
 .pine/tickets/FEAT-48hreg.md                       |    6 +
 .pine/tickets/FEAT-4d0bje.md                       |   62 +
 .pine/tickets/FEAT-53fht8.md                       |   60 +
 .pine/tickets/FEAT-55v09k.md                       |   82 +-
 .pine/tickets/FEAT-5fhj6p.md                       |   69 +
 .pine/tickets/FEAT-5fv8gf.md                       |    3 +
 .pine/tickets/FEAT-5kfctc.md                       |   66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   81 +-
 .pine/tickets/FEAT-5mvech.md                       |   72 +
 .pine/tickets/FEAT-5rvtzc.md                       |   91 +-
 .pine/tickets/FEAT-5s1w0t.md                       |   83 +-
 .pine/tickets/FEAT-5z37xh.md                       |   72 +
 .pine/tickets/FEAT-68zzqs.md                       |  438 +++
 .pine/tickets/FEAT-6vfn3s.md                       |  354 +-
 .pine/tickets/FEAT-7tgasa.md                       |   61 +
 .pine/tickets/FEAT-8qyfh1.md                       |  452 ++-
 .pine/tickets/FEAT-8r9n21.md                       |  304 +-
 .pine/tickets/FEAT-91as16.md                       |   93 +-
 .pine/tickets/FEAT-9dqn7d.md                       |  422 +++
 .pine/tickets/FEAT-9knk67.md                       |   84 +-
 .pine/tickets/FEAT-adzn0a.md                       |   74 +-
 .pine/tickets/FEAT-agj52c.md                       |   64 +
 .pine/tickets/FEAT-az620p.md                       |  447 ++-
 .pine/tickets/FEAT-bp0ytb.md                       |  338 +-
 .pine/tickets/FEAT-bscygc.md                       |   62 +
 .pine/tickets/FEAT-cjpbe6.md                       |   70 +
 .pine/tickets/FEAT-cpdp8y.md                       |   70 +
 .pine/tickets/FEAT-cwz4ac.md                       |   66 +
 .pine/tickets/FEAT-cx3hq1.md                       |   71 +
 .pine/tickets/FEAT-czbzs6.md                       |   65 +
 .pine/tickets/FEAT-ddzk2k.md                       |    6 +-
 .pine/tickets/FEAT-de8d4c.md                       |   71 +
 .pine/tickets/FEAT-ed6wdy.md                       |   66 +
 .pine/tickets/FEAT-frvez8.md                       |   70 +
 .pine/tickets/FEAT-g6wrxm.md                       |   64 +
 .pine/tickets/FEAT-gg85se.md                       |   69 +
 .pine/tickets/FEAT-gxppx1.md                       |   71 +
 .pine/tickets/FEAT-jq84xk.md                       |   67 +
 .pine/tickets/FEAT-jwhdsy.md                       |  411 ++-
 .pine/tickets/FEAT-k9dwgn.md                       |   65 +
 .pine/tickets/FEAT-kwxxd0.md                       |   71 +
 .pine/tickets/FEAT-m94hhx.md                       |   60 +
 .pine/tickets/FEAT-n19dch.md                       |   66 +
 .pine/tickets/FEAT-n5fdz3.md                       |  458 +++
 .pine/tickets/FEAT-nc6z9r.md                       |   68 +
 .pine/tickets/FEAT-nch9dg.md                       |   67 +
 .pine/tickets/FEAT-nrfg6e.md                       |   69 +
 .pine/tickets/FEAT-nrfz6m.md                       |   64 +
 .pine/tickets/FEAT-nxxbs5.md                       |   77 +
 .pine/tickets/FEAT-pd3p6x.md                       |   85 +-
 .pine/tickets/FEAT-pnbt4z.md                       |   91 +
 .pine/tickets/FEAT-ptyh9w.md                       |   65 +
 .pine/tickets/FEAT-q81bq4.md                       |  444 ++-
 .pine/tickets/FEAT-qcm5ec.md                       |   74 +-
 .pine/tickets/FEAT-qe6wb8.md                       |  342 +-
 .pine/tickets/FEAT-qfr9xe.md                       |  136 +
 .pine/tickets/FEAT-sar60r.md                       |   87 +-
 .pine/tickets/FEAT-sdjdh2.md                       |   33 +
 .pine/tickets/FEAT-sfy1tq.md                       |  139 +
 .pine/tickets/FEAT-snxxny.md                       |  409 +++
 .pine/tickets/FEAT-sp8cfm.md                       |  359 +-
 .pine/tickets/FEAT-ss44d9.md                       |   67 +
 .pine/tickets/FEAT-t26rt7.md                       |   65 +
 .pine/tickets/FEAT-v8k1tc.md                       |   88 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  395 +-
 .pine/tickets/FEAT-whn5vb.md                       |  101 +-
 .pine/tickets/FEAT-wkmv5e.md                       |   67 +
 .pine/tickets/FEAT-xeq6st.md                       |   68 +
 .pine/tickets/FEAT-xqqjqv.md                       |  327 ++
 .pine/tickets/FEAT-xr7ga9.md                       |   79 +
 .pine/tickets/FEAT-xx6p22.md                       |   62 +
 .pine/tickets/FEAT-ykyfbd.md                       |   72 +
 .pine/tickets/FEAT-yx0qt6.md                       |   71 +
 .pine/tickets/FEAT-za118x.md                       |   61 +
 .pine/tickets/FEAT-zmfsjd.md                       |   71 +
 .pine/tickets/FEAT-znm60y.md                       |  314 +-
 .pine/tickets/FEAT-ztxs5p.md                       |  345 +-
 Makefile                                           |   11 +
 README.md                                          |  221 +-
 cmd/kilasflow/main.go                              |  192 +-
 cmd/nodepackgen/generate.go                        |  576 +++
 cmd/nodepackgen/generate_test.go                   |  374 ++
 cmd/nodepackgen/main.go                            |  165 +
 cmd/nodepackgen/openapi.go                         |  168 +
 cmd/nodepackgen/testdata/manifest.json             |   12 +
 cmd/nodepackgen/testdata/pack.golden.json          |  238 ++
 cmd/nodepackgen/testdata/report.golden.md          |   20 +
 cmd/nodepackgen/testdata/spec.json                 |   98 +
 config.example.yaml                                |   17 +
 gflow-prd-v1.md                                    |   32 +
 internal/api/credentials_test.go                   |  357 ++
 internal/api/embed_test.go                         |   61 +-
 internal/api/handlers/credentials.go               |  298 ++
 internal/api/handlers/executions.go                |   40 +-
 internal/api/handlers/interop.go                   |    5 +-
 internal/api/handlers/nodes.go                     |  468 ++-
 internal/api/handlers/workflows.go                 |  118 +-
 internal/api/middleware/embed.go                   |   10 +-
 internal/api/node_types_test.go                    |  424 +++
 internal/api/routes.go                             |   54 +-
 internal/api/server.go                             |   30 +-
 internal/api/workflows_test.go                     |  136 +-
 internal/binary/binary.go                          |  212 ++
 internal/binary/binary_test.go                     |  210 ++
 internal/conditions/conditions.go                  |  542 +++
 internal/conditions/conditions_test.go             |  231 ++
 internal/conditions/doc.go                         |   26 +
 internal/config/config.go                          |  101 +-
 internal/config/config_test.go                     |   35 +
 internal/credentials/builtin.go                    |  153 +
 internal/credentials/credentials.go                |   98 +-
 internal/credentials/credentials_test.go           |  242 ++
 internal/credentials/registry.go                   |  340 ++
 internal/datetime/datetime_test.go                 |  134 +
 internal/datetime/doc.go                           |   15 +
 internal/datetime/format.go                        |  195 +
 internal/datetime/parse.go                         |  108 +
 internal/engine/authenticate.go                    |   95 +
 internal/engine/runner.go                          |  465 ++-
 internal/engine/runner_test.go                     |  579 ++-
 internal/engine/service.go                         |  318 +-
 internal/engine/service_test.go                    |   33 +-
 internal/engine/subworkflow_test.go                |  329 ++
 internal/engine/worker_test.go                     |    5 +
 internal/execution/records.go                      |   41 +-
 internal/expression/doc.go                         |   82 +-
 internal/expression/expression.go                  |  350 +-
 internal/expression/expression_test.go             |  374 +-
 internal/expression/functions.go                   |  219 ++
 internal/expression/roots.go                       |  125 +
 internal/expression/undefined.go                   |   22 +
 internal/interop/n8n/corpus/BASELINE.md            |   43 +-
 internal/interop/n8n/corpus/baseline.json          |  115 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |  107 +-
 internal/interop/n8n/n8n.go                        |  480 ++-
 internal/interop/n8n/n8n_test.go                   | 1423 +++++++-
 internal/interop/n8n/parameters.go                 | 1665 ++++++++-
 internal/loadoptions/loadoptions.go                |  412 +++
 internal/loadoptions/loadoptions_test.go           |  453 +++
 internal/loadoptions/schema.go                     |   68 +
 internal/loadoptions/sql.go                        |  287 ++
 internal/loadoptions/sql_test.go                   |  350 ++
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  716 +++-
 internal/node/registry_test.go                     |  816 ++++-
 internal/nodepack/nodepack.go                      |  424 +++
 internal/nodepack/startcase.go                     |  136 +
 internal/nodepack/startcase_test.go                |   82 +
 internal/nodepack/trigger.go                       |  433 +++
 internal/property/loader.go                        |   92 +
 internal/property/locator_test.go                  |  116 +
 internal/property/mapper.go                        |  322 ++
 internal/property/mapper_test.go                   |  196 +
 internal/property/property.go                      |  459 +++
 internal/property/testdata/visibility.json         |  163 +
 internal/property/visibility.go                    |  315 ++
 internal/property/visibility_test.go               |   77 +
 internal/repository/executions.go                  |  135 +
 internal/repository/models.go                      |   71 +-
 internal/repository/schedules.go                   |  140 +-
 internal/repository/webhooks.go                    |   17 +-
 internal/repository/workflows.go                   |   66 +-
 internal/routing/doc.go                            |   54 +
 internal/routing/executor.go                       |  571 +++
 internal/routing/request.go                        |  412 +++
 internal/routing/response.go                       |  177 +
 internal/routing/routing.go                        |  373 ++
 internal/routing/routing_test.go                   |  764 ++++
 internal/runcode/doc.go                            |   37 +-
 internal/scheduler/extract.go                      |   81 +
 internal/scheduler/item.go                         |   71 +
 internal/scheduler/rule.go                         |  321 ++
 internal/scheduler/rule_test.go                    |  278 ++
 internal/scheduler/scheduler.go                    |  139 +-
 internal/scheduler/scheduler_test.go               |  153 +
 internal/sqlbuild/sqlbuild.go                      |  363 ++
 internal/sqlbuild/sqlbuild_test.go                 |  617 ++++
 .../testdata/fuzz/FuzzIdentifier/074b649c870c7bf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/0ef06ad2d24324a6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/10abc61b4419477c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/143ed5c16fb0545a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1bb2624596902fcc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1d590be519b71d94  |    2 +
 .../testdata/fuzz/FuzzIdentifier/25f9d5a362559265  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e6e50da573e69b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34b4da777d929731  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3903663d1e91caeb  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3d501ac59ad587c9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4415ff0d1b9299f1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/46fa4fad84220ea4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4935e94ff365ef93  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ddf220b34d80659  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5ac14d4f3feba852  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b4f050cad7d6971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5df9ba84197819df  |    2 +
 .../testdata/fuzz/FuzzIdentifier/60aff40e01913abe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66542488bb28dac3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66889c986f27ea1d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6ad80d0a2c06ac54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7ad7e6cfe18eb7fe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e151b9450d2b761  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8378d0fed0b8996b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8fff7f2dd3835c42  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9633d0ce6f89b127  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c3612f616e75c30  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9f72fab7a7931bc4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a4ce8510de4da361  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a5833806503a0d20  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a595c2930c69a903  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b128ad1323911a0d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b41d082073a2bebc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b48b96f658bda199  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b70f41cf6885f569  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ca57023a06a80b6f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cbb1fd2d98956b9f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cf37f09b95c8bad1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d1a9e9faf54d8eb4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dc8d3d87a20bf6bd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dcad439aaec6c2f6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2b8af39f88d630c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e758baca4af7c7b3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ec1cb26f1a43547c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ee4745c7324edeaf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f3a15bf1ad31abb1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f5b1055fe63bbb4f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f6c3d89cb56eb2f0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd22c094ae02673c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fe2500ec53808c61  |    2 +
 .../sqlbuild/testdata/postgres/delete_drop.sql     |    1 +
 .../sqlbuild/testdata/postgres/delete_rows.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_truncate.sql |    1 +
 internal/sqlbuild/testdata/postgres/insert.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select_all.sql |    2 +
 .../sqlbuild/testdata/postgres/select_null.sql     |    3 +
 internal/sqlbuild/testdata/postgres/update.sql     |    3 +
 internal/sqlbuild/testdata/postgres/upsert.sql     |    3 +
 internal/sqlnode/introspect.go                     |  240 ++
 internal/sqlnode/sqlnode.go                        |  341 +-
 internal/sqlnode/sqlnode_test.go                   |  106 +
 internal/webhook/export_test.go                    |   14 +
 internal/webhook/lifecycle.go                      |  304 ++
 internal/webhook/lifecycle_test.go                 |  229 ++
 internal/webhook/request_lifecycle.go              |  207 ++
 internal/webhook/shape.go                          |  248 ++
 internal/webhook/shape_test.go                     |  211 ++
 internal/webhook/webhook.go                        |  261 +-
 internal/webhook/webhook_test.go                   |  225 +-
 internal/workflow/compiler.go                      |  310 +-
 internal/workflow/compiler_test.go                 |   97 +
 internal/workflow/document.go                      |   81 +-
 internal/workflow/document_test.go                 |  360 +-
 internal/workflow/typeversion.go                   |   10 +
 nodes/ai.go                                        |   54 +-
 nodes/annotation.go                                |    3 +
 nodes/assignments.go                               |  180 +
 nodes/bindings_test.go                             |  129 +
 nodes/code.go                                      |   93 +-
 nodes/code_test.go                                 |  126 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  186 +-
 nodes/database.go                                  |  255 +-
 nodes/database_test.go                             |  534 ++-
 nodes/datetime.go                                  |  408 +++
 nodes/datetime_test.go                             |  274 ++
 nodes/executors.go                                 |  604 ++-
 nodes/executors_test.go                            |  480 +++
 nodes/flow.go                                      |  457 +++
 nodes/flow_test.go                                 |  464 +++
 nodes/http.go                                      |  195 +-
 nodes/http_test.go                                 |  225 ++
 nodes/jscode.go                                    |  172 +
 nodes/jscode_test.go                               |  100 +
 nodes/loop.go                                      |  245 ++
 nodes/postgres_v2.go                               |  462 +++
 nodes/postgres_v2_test.go                          |  231 ++
 nodes/presentation_test.go                         |   60 +
 nodes/routing.go                                   |   23 +
 nodes/subworkflow.go                               |  275 ++
 nodes/telegram.go                                  |  393 ++
 nodes/telegram_download.go                         |  243 ++
 nodes/telegram_lifecycle.go                        |  423 +++
 nodes/telegram_test.go                             |  610 ++++
 nodes/transform.go                                 |  745 ++++
 nodes/transform_test.go                            |  315 ++
 nodes/unsupported.go                               |    4 +
 nodes/wait.go                                      |  227 ++
 nodes/webhook.go                                   |  290 +-
 packs/telegram/README.md                           |   40 +
 packs/telegram/pack.json                           | 1119 ++++++
 packs/telegram/telegram.go                         |   58 +
 packs/telegram/telegram_test.go                    |  466 +++
 packs/waha/README.md                               |   32 +
 packs/waha/REPORT-202409.md                        |  100 +
 packs/waha/REPORT-202502.md                        |  128 +
 packs/waha/manifest-202409.json                    |  124 +
 packs/waha/manifest-202502.json                    |  124 +
 packs/waha/pack-202409.json                        | 2794 ++++++++++++++
 packs/waha/pack-202502.json                        | 3844 ++++++++++++++++++++
 packs/waha/pack-trigger-202409.json                |  124 +
 packs/waha/pack-trigger-202502.json                |  130 +
 packs/waha/waha.go                                 |  107 +
 packs/waha/waha_test.go                            | 1196 ++++++
 sdk/src/generated/models.ts                        |  720 +++-
 .../lib/api/generated/credentials/credentials.ts   |  199 +-
 .../lib/api/generated/models/activationNotice.ts   |   13 +
 .../lib/api/generated/models/activationResource.ts |   23 +
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 web/src/lib/api/generated/models/condition.ts      |   14 +
 .../api/generated/models/credentialRequirement.ts  |   15 +
 web/src/lib/api/generated/models/definition.ts     |   17 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/executionResource.ts  |    2 +
 .../lib/api/generated/models/executionSummary.ts   |    2 +
 .../lib/api/generated/models/expressionGrammar.ts  |   16 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |   19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |   15 +
 web/src/lib/api/generated/models/index.ts          |   27 +
 .../api/generated/models/loadOptionsInputBody.ts   |   22 +
 .../models/loadOptionsInputBodyParameters.ts       |    9 +
 .../api/generated/models/loadOptionsResource.ts    |   17 +
 .../lib/api/generated/models/loadSchemaResource.ts |   17 +
 web/src/lib/api/generated/models/mapperColumn.ts   |   21 +
 web/src/lib/api/generated/models/nodeCodex.ts      |   16 +
 .../api/generated/models/nodeCodexSubcategories.ts |    9 +
 web/src/lib/api/generated/models/nodeIcon.ts       |   12 +
 web/src/lib/api/generated/models/option.ts         |   12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |   21 +
 web/src/lib/api/generated/models/port.ts           |    9 +-
 .../lib/api/generated/models/propertyDefinition.ts |   19 +
 web/src/lib/api/generated/models/propertyGroup.ts  |   15 +
 web/src/lib/api/generated/models/propertyMode.ts   |   19 +
 .../generated/models/resourceMapperDeclaration.ts  |   15 +
 .../api/generated/models/testCredentialResource.ts |   17 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
 web/src/lib/api/generated/models/visibility.ts     |   15 +
 .../lib/api/generated/models/webhookDeclaration.ts |   15 +
 web/src/lib/api/generated/nodes/nodes.ts           |  443 ++-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    3 +-
 .../components/workflow-editor/canvas-node.svelte  |   33 +-
 .../workflow-editor/execution-canvas-node.svelte   |   20 +-
 .../components/workflow-editor/node-icon.svelte    |   10 +-
 .../components/workflow-editor/node-picker.svelte  |    8 +-
 .../workflow-editor/properties-panel.svelte        |   61 +-
 .../workflow-editor/property-field.svelte          |  493 ++-
 .../workflow-editor/workflow-editor.svelte         |    4 +-
 web/src/lib/workflow-editor/assignments.test.ts    |   82 +
 web/src/lib/workflow-editor/assignments.ts         |   89 +
 web/src/lib/workflow-editor/conditions.test.ts     |   82 +
 web/src/lib/workflow-editor/conditions.ts          |   80 +
 web/src/lib/workflow-editor/credentials.ts         |   41 +-
 web/src/lib/workflow-editor/document.test.ts       |   10 +-
 web/src/lib/workflow-editor/document.ts            |    4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |    2 +-
 web/src/lib/workflow-editor/execution.test.ts      |   72 +-
 web/src/lib/workflow-editor/execution.ts           |   75 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   43 +
 .../lib/workflow-editor/fixed-collection.test.ts   |  131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |   75 +
 web/src/lib/workflow-editor/node-visual.test.ts    |  163 +-
 web/src/lib/workflow-editor/node-visual.ts         |  220 +-
 web/src/lib/workflow-editor/ports.test.ts          |   98 +-
 web/src/lib/workflow-editor/ports.ts               |   34 +-
 .../lib/workflow-editor/resource-locator.test.ts   |  112 +
 web/src/lib/workflow-editor/resource-locator.ts    |  100 +
 .../lib/workflow-editor/resource-mapper.test.ts    |  121 +
 web/src/lib/workflow-editor/resource-mapper.ts     |  111 +
 web/src/lib/workflow-editor/visibility.test.ts     |   35 +
 web/src/lib/workflow-editor/visibility.ts          |  199 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   26 +
 392 files changed, 63704 insertions(+), 1690 deletions(-)
```
