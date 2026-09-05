---
id: FEAT-9dqn7d
title: Close the five real gaps in the SQL node
status: done
priority: high
labels:
    - nodes
    - parity
    - sql
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T12:41:23Z"
---

## Scope

`DatabaseExecutor.Execute` calls `runOne` once per input item (`nodes/database.go:203-213`), so a hundred-row insert is a hundred statements, and `sqlnode.Open` pins the pool with `db.SetMaxOpenConns(1)` (`internal/sqlnode/sqlnode.go:100`; the roadmap's 99-101 spans the comment above it), so they cannot even overlap. The loop is why there are N round trips; the pin is why they are serial.

`Connection.Transaction` runs every statement through `tx.ExecContext` (`internal/sqlnode/sqlnode.go:368`, not 367 as the roadmap has it) and returns `Rows: []map[string]any{}` for each, so a transaction can never hand back a row: `INSERT … RETURNING id` commits and the generated id is discarded, leaving only the summary item's `rowsAffected`, `statements` and `"committed": true` (`nodes/database.go:264-268`).

The `parameters` property carries no `VisibleWhen` (`nodes/database.go:71-74`) while the three statement properties each carry one, and the editor filters on that field alone (`web/src/lib/components/workflow-editor/properties-panel.svelte:36`), so Parameters is shown for every operation. `runOne` decodes it at `nodes/database.go:222` but uses `bound` only in the query and execute branches, so a malformed array fails a transaction that would have ignored it.

`maxRows` and `timeoutSeconds` come from the per-item resolved parameters with no server-side ceiling (`nodes/database.go:218-221`), and `Limits.MaxRows` is the only bound on the row buffer. The roadmap's `{{ 500000000 }}` is not the vector — `parse` refuses a body that does not start with `$` (`internal/expression/expression.go:217-219`) — but `{{ $json.maxRows }}` over a webhook body is, since a whole-expression marker returns the looked-up value with its own type.

`timeoutSeconds` is declared twice: "Statement timeout (seconds)" at `Default: 30` in Parameters (`nodes/database.go:75`) and "Timeout (seconds)" at `Default: 0` in `sharedSettings` (`nodes/core.go:132`; the roadmap says 124). `validateProperties` cannot see the collision, because `validateDefinition` calls it once per group with a fresh `seen` map (`internal/node/registry.go:220`, `:223`, `:241`), and at run time the two land in different stores — one bounds a statement, the other the whole node through `nodeContext` in `internal/engine/runner.go`, which reads `node.Settings["timeoutSeconds"]` — while the form shows two boxes that disagree.

Bulk write is the commonest thing asked of a database node, and this is the family where a customer's own data lives. Each of the five is met in the first hour of use, and none of them needs a new property kind, a new endpoint or a version bump.

## Acceptance criteria

- [x] An execute over 500 input items runs on one prepared statement inside one transaction, proven by a test that fails a middle statement and finds nothing committed.
- [x] The per-item output of an execute is unchanged — one `rowsAffected` item per input item — pinned by a test so existing downstream nodes see the same stream.
- [x] A transaction statement declared as returning rows yields those rows as items, so `INSERT … RETURNING id` reaches the next node, proven by a SQLite test.
- [x] Setting `parameters` on a transaction is refused at validation with a message naming the per-statement field, rather than silently ignored, proven by a validator test.
- [x] `maxRows` and `timeoutSeconds` arriving from an expression are clamped to the configured ceiling and the clamp is marked on the output item, proven by a test driving both from `$json`.
- [x] `validateProperties` rejects any definition declaring one key in both Parameters and SharedSettings, and the database and HTTP nodes both pass, proven by `go test ./...`. **Three nodes were colliding, not two** — Code as well.
- [x] The PostgreSQL half of the batching and returning-rows coverage is run by hand with `KILASFLOW_TEST_POSTGRES_DSN` set and its output recorded here. **MySQL too**, for the reason in Outcome.

## Outcome

### The duplicated key, taken first as planned

The plan said start here because the answer decides whose rename this ticket carries, and it carried one more than expected: **Code declares `timeoutSeconds` as well**, so the three nodes with their own timeout were all colliding with the shared setting of the same name. They are now `statementTimeoutSeconds`, `requestTimeoutSeconds` and `scriptTimeoutSeconds`, and `validateGroupsDoNotCollide` refuses the next one at registration.

Only the top level is compared. A collection's inner field and a shared setting live in different objects and never meet — the same reason `validateProperties` does not share its `seen` map across the recursion.

**The rename is not a stored-document migration.** A workflow saved before it still carries `timeoutSeconds` in its parameters, and `timeoutParameter` reads both, new key winning. Rewriting every saved document to correct a parameter name is a far larger and riskier change than reading two keys in one helper. The consequence worth stating: an old document keeps its configured timeout until the first time someone saves that node, at which point the form's own value takes over — which is the value that author was looking at, so the form and the run agree from then on.

### Bulk writes

One transaction around the item loop, one prepared statement inside it, each item's parameters through the prepared handle. The pinned single connection is an asset, exactly as the plan said: a prepared statement stays on the connection it was built for.

**The output shape is deliberately unchanged** — one `rowsAffected` item per input item, in order. The batch is meant to be invisible downstream, and a test pins that rather than only the new atomicity.

Two decisions the plan did not settle:

- **The statement text can differ per item**, because it may be built from an expression. The prepared handle is rebuilt when the text changes and reused while it stays the same, so the ordinary case is one prepare for five hundred items and the unusual case is still one transaction. A batch that covered only some of the items would be neither the old behaviour nor atomic.
- **A run with mixed operations falls back to the per-item loop.** Operation is a dropdown, so in practice it is constant, but it is resolved like any other parameter.

The timeout **bounds each statement, not the batch**, which is what a field called "statement timeout" says it does; the batch as a whole is bounded by the node's own timeout setting and the execution's. Making it bound the batch would have quietly turned a working 500-row insert into a timeout for anyone whose per-row time was fine.

The error names the item: `item 251 failed and the batch was rolled back`. "Statement failed" over five hundred items says nothing about which row was wrong.

### Rows out of a transaction

`Returning` is declared per statement in the JSON, never sniffed from the SQL — comments, CTEs and `WITH … RETURNING` all defeat the heuristic, and the answer differs per driver.

**The trap the plan named is real and is now a test.** Routing every statement through `QueryContext` looks correct: a non-returning statement comes back as a result set with no columns and no reachable `RowsAffected`, so every transaction in the installation would keep saying `"committed": true` while `rowsAffected` silently became zero. The transaction test asserts the summary still adds to two — the returned row plus the updated one — so that regression cannot pass.

Rows come first and the summary last, in statement order.

### The ceiling

`{{ 500000000 }}` is not the vector — `parse` refuses a body that does not start with `$` — but `{{ $json.maxRows }}` over a webhook body is, and that is what the test drives. The ceiling lives in a one-word `sql` config section, because `envKeyToPath` cuts an environment key at its first underscore and a two-word section could never be overridden; a test proves `KILASFLOW_SQL_MAX_ROWS` reaches it.

It **clamps rather than refuses**, and says so with a `$clamped` item beside the existing `$truncated` one: a workflow asking for more rows than the deployment allows still wants the rows it can have, and a run that quietly returned fewer than it was asked for is how a partial read gets mistaken for a complete one.

`Ceiling.Apply` does not raise an unset limit to the ceiling. Zero means "the node configured nothing", which the statement paths already turn into their own defaults; clamping it would turn an unset limit into a ceiling-sized one.

Threaded through `RegisterExecutors` as a **variadic option** rather than a seventh positional parameter. The signature already carries six, and forty-odd call sites would have had to be edited to pass a value almost none of them care about.

### Parameters on a transaction

Refused at validation, naming the per-statement field. The plan's recommendation is kept: the field stays visible, because `VisibilityCondition` is single-key equality AND-ed together so "shown unless transaction" is not expressible, and forking it into `queryParameters` and `executeParameters` would rewrite every stored document for a cosmetic gain.

The check accepts every shape `boundParameters` does. The editor stores this field as a string, but a document posted to the API can hold a real JSON array, and a check that only looked at strings would let exactly that shape through.

### Live servers

The gated coverage was **run, not deferred** — Docker is on this machine, so a throwaway server costs a minute. `KILASFLOW_TEST_MYSQL_DSN` is new, and the test is parameterised over both drivers rather than copied.

MySQL is not scope creep here: this change makes DDL run inside a prepared transaction, and MySQL is the one shipped driver that treats DDL as an implicit commit. Reasoning about that is worse than running it.

```
$ KILASFLOW_TEST_POSTGRES_DSN=... KILASFLOW_TEST_MYSQL_DSN=...     go test ./nodes/ -run TestALiveServer -v -count=1
=== RUN   TestALiveServerBatchesAtomicallyAndReturnsRows
=== RUN   TestALiveServerBatchesAtomicallyAndReturnsRows/postgres
=== RUN   TestALiveServerBatchesAtomicallyAndReturnsRows/mysql
--- PASS: TestALiveServerBatchesAtomicallyAndReturnsRows (0.19s)
    --- PASS: TestALiveServerBatchesAtomicallyAndReturnsRows/postgres (0.12s)
    --- PASS: TestALiveServerBatchesAtomicallyAndReturnsRows/mysql (0.07s)
PASS
ok      github.com/kilaslabs/kilas-flow/nodes   0.498s
```

PostgreSQL 16.14, MySQL 8.4.11. The whole suite was then run with both gates set and is green, including `internal/database`'s own PostgreSQL migration coverage. MySQL has no `RETURNING`, so its half checks what applies there: the transaction still commits and still counts its rows.

The commands are recorded in `.pine/memory/live-databases.md`, because eight p6 tickets ask for the same thing.

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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `4816f2ed` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `cf72f8d0` — chore(pine): open phase p9 for the Datastore and route database parity to p4
- Files changed (base → working tree):

```
 .pine/MEMORY.md                                    |    2 +
 .pine/roadmap.md                                   |  252 ++
 .pine/tickets/EPIC-m42s3g.md                       |   10 +-
 .pine/tickets/FEAT-0556ck.md                       |   66 +
 .pine/tickets/FEAT-0f87fn.md                       |  300 +-
 .pine/tickets/FEAT-12s0e5.md                       |   65 +
 .pine/tickets/FEAT-1axhdn.md                       |   65 +
 .pine/tickets/FEAT-1c70nt.md                       |   73 +
 .pine/tickets/FEAT-27km39.md                       |   71 +
 .pine/tickets/FEAT-2f68r8.md                       |   81 +-
 .pine/tickets/FEAT-2phs15.md                       |   68 +
 .pine/tickets/FEAT-3taswf.md                       |   67 +
 .pine/tickets/FEAT-3xqky1.md                       |   70 +
 .pine/tickets/FEAT-45tfmh.md                       |   68 +
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
 .pine/tickets/FEAT-68zzqs.md                       |   65 +
 .pine/tickets/FEAT-6vfn3s.md                       |  354 +-
 .pine/tickets/FEAT-7tgasa.md                       |   61 +
 .pine/tickets/FEAT-8r9n21.md                       |  304 +-
 .pine/tickets/FEAT-91as16.md                       |   93 +-
 .pine/tickets/FEAT-9dqn7d.md                       |  137 +
 .pine/tickets/FEAT-9knk67.md                       |   84 +-
 .pine/tickets/FEAT-adzn0a.md                       |   74 +-
 .pine/tickets/FEAT-agj52c.md                       |   64 +
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
 .pine/tickets/FEAT-kwxxd0.md                       |   64 +
 .pine/tickets/FEAT-m94hhx.md                       |   60 +
 .pine/tickets/FEAT-n19dch.md                       |   66 +
 .pine/tickets/FEAT-n5fdz3.md                       |   69 +
 .pine/tickets/FEAT-nc6z9r.md                       |   68 +
 .pine/tickets/FEAT-nch9dg.md                       |   67 +
 .pine/tickets/FEAT-nrfg6e.md                       |   69 +
 .pine/tickets/FEAT-nrfz6m.md                       |   64 +
 .pine/tickets/FEAT-nxxbs5.md                       |   77 +
 .pine/tickets/FEAT-pd3p6x.md                       |   85 +-
 .pine/tickets/FEAT-pnbt4z.md                       |   91 +
 .pine/tickets/FEAT-ptyh9w.md                       |   65 +
 .pine/tickets/FEAT-qcm5ec.md                       |   74 +-
 .pine/tickets/FEAT-qe6wb8.md                       |  342 +-
 .pine/tickets/FEAT-qfr9xe.md                       |  136 +
 .pine/tickets/FEAT-sar60r.md                       |   87 +-
 .pine/tickets/FEAT-sdjdh2.md                       |   33 +
 .pine/tickets/FEAT-sfy1tq.md                       |   63 +
 .pine/tickets/FEAT-snxxny.md                       |   68 +
 .pine/tickets/FEAT-sp8cfm.md                       |  359 +-
 .pine/tickets/FEAT-ss44d9.md                       |   67 +
 .pine/tickets/FEAT-t26rt7.md                       |   65 +
 .pine/tickets/FEAT-v8k1tc.md                       |   88 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  395 +-
 .pine/tickets/FEAT-whn5vb.md                       |  101 +-
 .pine/tickets/FEAT-wkmv5e.md                       |   67 +
 .pine/tickets/FEAT-xeq6st.md                       |   68 +
 .pine/tickets/FEAT-xqqjqv.md                       |  327 ++
 .pine/tickets/FEAT-xr7ga9.md                       |   76 +
 .pine/tickets/FEAT-xx6p22.md                       |   62 +
 .pine/tickets/FEAT-ykyfbd.md                       |   72 +
 .pine/tickets/FEAT-yx0qt6.md                       |   71 +
 .pine/tickets/FEAT-za118x.md                       |   61 +
 .pine/tickets/FEAT-zmfsjd.md                       |   71 +
 .pine/tickets/FEAT-znm60y.md                       |  314 +-
 .pine/tickets/FEAT-ztxs5p.md                       |  345 +-
 Makefile                                           |   11 +
 README.md                                          |   33 +
 cmd/kilasflow/main.go                              |  121 +-
 cmd/nodepackgen/generate.go                        |  576 +++
 cmd/nodepackgen/generate_test.go                   |  374 ++
 cmd/nodepackgen/main.go                            |  165 +
 cmd/nodepackgen/openapi.go                         |  168 +
 cmd/nodepackgen/testdata/manifest.json             |   12 +
 cmd/nodepackgen/testdata/pack.golden.json          |  238 ++
 cmd/nodepackgen/testdata/report.golden.md          |   20 +
 cmd/nodepackgen/testdata/spec.json                 |   98 +
 config.example.yaml                                |   17 +
 internal/api/handlers/credentials.go               |   73 +
 internal/api/handlers/interop.go                   |    5 +-
 internal/api/handlers/nodes.go                     |  262 +-
 internal/api/handlers/workflows.go                 |  113 +-
 internal/api/middleware/embed.go                   |   10 +-
 internal/api/routes.go                             |   10 +-
 internal/api/server.go                             |   15 +-
 internal/api/workflows_test.go                     |    4 +-
 internal/binary/binary.go                          |  212 ++
 internal/binary/binary_test.go                     |  210 ++
 internal/conditions/conditions.go                  |  542 +++
 internal/conditions/conditions_test.go             |  231 ++
 internal/conditions/doc.go                         |   26 +
 internal/config/config.go                          |   61 +
 internal/config/config_test.go                     |   35 +
 internal/credentials/builtin.go                    |  153 +
 internal/credentials/credentials.go                |   98 +-
 internal/credentials/credentials_test.go           |  242 ++
 internal/credentials/registry.go                   |  340 ++
 internal/engine/authenticate.go                    |   95 +
 internal/engine/runner.go                          |  418 ++-
 internal/engine/runner_test.go                     |  579 ++-
 internal/engine/service.go                         |   45 +-
 internal/engine/service_test.go                    |   33 +-
 internal/execution/records.go                      |   27 +-
 internal/expression/doc.go                         |   82 +-
 internal/expression/expression.go                  |  328 +-
 internal/expression/expression_test.go             |  309 +-
 internal/expression/functions.go                   |  219 ++
 internal/expression/roots.go                       |  125 +
 internal/expression/undefined.go                   |   22 +
 internal/interop/n8n/corpus/BASELINE.md            |   42 +-
 internal/interop/n8n/corpus/baseline.json          |  114 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |   64 +-
 internal/interop/n8n/n8n.go                        |  378 +-
 internal/interop/n8n/n8n_test.go                   |  798 +++-
 internal/interop/n8n/parameters.go                 |  891 ++++-
 internal/loadoptions/loadoptions.go                |  353 ++
 internal/loadoptions/loadoptions_test.go           |  354 ++
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  661 +++-
 internal/node/registry_test.go                     |  647 +++-
 internal/nodepack/nodepack.go                      |  424 +++
 internal/nodepack/startcase.go                     |  136 +
 internal/nodepack/startcase_test.go                |   82 +
 internal/nodepack/trigger.go                       |  433 +++
 internal/property/loader.go                        |   92 +
 internal/property/property.go                      |  299 ++
 internal/property/testdata/visibility.json         |  163 +
 internal/property/visibility.go                    |  315 ++
 internal/property/visibility_test.go               |   77 +
 internal/repository/executions.go                  |    2 +
 internal/repository/models.go                      |   37 +-
 internal/repository/webhooks.go                    |   17 +-
 internal/routing/doc.go                            |   54 +
 internal/routing/executor.go                       |  571 +++
 internal/routing/request.go                        |  412 +++
 internal/routing/response.go                       |  177 +
 internal/routing/routing.go                        |  373 ++
 internal/routing/routing_test.go                   |  764 ++++
 internal/sqlnode/sqlnode.go                        |  213 +-
 internal/sqlnode/sqlnode_test.go                   |   61 +
 internal/webhook/export_test.go                    |   14 +
 internal/webhook/lifecycle.go                      |  304 ++
 internal/webhook/lifecycle_test.go                 |  229 ++
 internal/webhook/request_lifecycle.go              |  207 ++
 internal/webhook/shape.go                          |  248 ++
 internal/webhook/shape_test.go                     |  211 ++
 internal/webhook/webhook.go                        |  187 +-
 internal/webhook/webhook_test.go                   |   48 +-
 internal/workflow/compiler.go                      |  275 +-
 internal/workflow/compiler_test.go                 |   97 +
 internal/workflow/document.go                      |   81 +-
 internal/workflow/document_test.go                 |  360 +-
 internal/workflow/typeversion.go                   |   10 +
 nodes/ai.go                                        |   54 +-
 nodes/annotation.go                                |    3 +
 nodes/assignments.go                               |  180 +
 nodes/bindings_test.go                             |  129 +
 nodes/code.go                                      |   30 +-
 nodes/code_test.go                                 |   66 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  177 +-
 nodes/database.go                                  |  206 +-
 nodes/database_test.go                             |  534 ++-
 nodes/executors.go                                 |  597 ++-
 nodes/executors_test.go                            |  480 +++
 nodes/flow.go                                      |  457 +++
 nodes/flow_test.go                                 |  464 +++
 nodes/http.go                                      |  195 +-
 nodes/http_test.go                                 |  225 ++
 nodes/loop.go                                      |  245 ++
 nodes/presentation_test.go                         |   60 +
 nodes/routing.go                                   |   23 +
 nodes/telegram.go                                  |  393 ++
 nodes/telegram_download.go                         |  243 ++
 nodes/telegram_lifecycle.go                        |  423 +++
 nodes/telegram_test.go                             |  610 ++++
 nodes/transform.go                                 |  745 ++++
 nodes/transform_test.go                            |  315 ++
 nodes/unsupported.go                               |    4 +
 nodes/webhook.go                                   |   35 +-
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
 sdk/src/generated/models.ts                        |  532 ++-
 .../lib/api/generated/credentials/credentials.ts   |   96 +-
 .../lib/api/generated/models/activationNotice.ts   |   13 +
 .../lib/api/generated/models/activationResource.ts |   23 +
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 web/src/lib/api/generated/models/condition.ts      |   14 +
 .../api/generated/models/credentialRequirement.ts  |   15 +
 web/src/lib/api/generated/models/definition.ts     |   16 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/expressionGrammar.ts  |   16 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |   19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |   15 +
 web/src/lib/api/generated/models/index.ts          |   21 +
 .../api/generated/models/loadOptionsInputBody.ts   |   20 +
 .../models/loadOptionsInputBodyParameters.ts       |    9 +
 .../api/generated/models/loadOptionsResource.ts    |   17 +
 web/src/lib/api/generated/models/nodeCodex.ts      |   16 +
 .../api/generated/models/nodeCodexSubcategories.ts |    9 +
 web/src/lib/api/generated/models/nodeIcon.ts       |   12 +
 web/src/lib/api/generated/models/option.ts         |   12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |   21 +
 web/src/lib/api/generated/models/port.ts           |    9 +-
 .../lib/api/generated/models/propertyDefinition.ts |   14 +
 web/src/lib/api/generated/models/propertyGroup.ts  |   15 +
 .../api/generated/models/testCredentialResource.ts |   14 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
 web/src/lib/api/generated/models/visibility.ts     |   15 +
 .../lib/api/generated/models/webhookDeclaration.ts |   15 +
 web/src/lib/api/generated/nodes/nodes.ts           |  342 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    3 +-
 .../components/workflow-editor/canvas-node.svelte  |   33 +-
 .../workflow-editor/execution-canvas-node.svelte   |   20 +-
 .../components/workflow-editor/node-icon.svelte    |   10 +-
 .../components/workflow-editor/node-picker.svelte  |    8 +-
 .../workflow-editor/properties-panel.svelte        |   48 +-
 .../workflow-editor/property-field.svelte          |  203 +-
 .../workflow-editor/workflow-editor.svelte         |    4 +-
 web/src/lib/workflow-editor/assignments.test.ts    |   82 +
 web/src/lib/workflow-editor/assignments.ts         |   89 +
 web/src/lib/workflow-editor/credentials.ts         |   41 +-
 web/src/lib/workflow-editor/document.test.ts       |   10 +-
 web/src/lib/workflow-editor/document.ts            |    4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |    2 +-
 web/src/lib/workflow-editor/execution.test.ts      |   72 +-
 web/src/lib/workflow-editor/execution.ts           |   75 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   43 +
 web/src/lib/workflow-editor/node-visual.test.ts    |  163 +-
 web/src/lib/workflow-editor/node-visual.ts         |  214 +-
 web/src/lib/workflow-editor/ports.test.ts          |   98 +-
 web/src/lib/workflow-editor/ports.ts               |   34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |   35 +
 web/src/lib/workflow-editor/visibility.ts          |  199 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   26 +
 272 files changed, 46265 insertions(+), 1366 deletions(-)
```
