---
id: FEAT-cjpbe6
title: Isolate Datastore data from redaction, traces and other tenants
status: done
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-nrfg6e
    - FEAT-1br8at
    - FEAT-a94c8y
    - FEAT-nrfz6m
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-06T05:40:09Z"
---

## Scope

`payload` in `internal/repository/executions.go:605-611` is the funnel every durable execution and node-run write passes through, and it ends in `execution.Redact(encoded)`; `internal/events/events.go:139` applies the same `Redact` to every live event's `Data`. `Redact` decides by key, never by content: `sensitiveKeys` at `internal/execution/redact.go:16-54` holds `"token"`, `"secret"`, `"apikey"`, `"password"`, `"credential"`, `"signature"`, `"cookie"` and twenty-one more, `normalizeKey` (155-166) strips `-`, `_`, space and `.` before lowercasing, and `isSensitiveKey` (141-153) strips one leading `x`. A datastore column legally named `api_key` normalises to `apikey` and is stored as `"[redacted]"`.

The second rule is sharper. `headerPairIsSensitive` (129-139) finds an object with a `name` key whose *value* names a sensitive header, then rewrites the sibling `value` key. The use case the roadmap calls the most popular for this feature is a cross-run key-value store — columns `name` and `value`. A row `{"name": "cookie", "value": "chocolate chip"}` lands in the trace as `{"name": "cookie", "value": "[redacted]"}`, with no error anywhere.

The roadmap's other premises describe code that V2-p1-6 — `FEAT-1br8at`, done — already removed, and are corrected here. `looksLikeCredential` is gone; `redact.go:56-67` records why. `session`, `sessionid`, `otp` and `pin` are deliberately absent (41-46, 51-53). And `internal/webhook/webhook.go:252` redacts only the header map, so `{"pin":"482913"}` reaches the first node intact. What remains is the durable write and the live stream.

`internal/engine/service.go:140-187` marshals `run.Input` and `run.Output` verbatim into every node-run row and publishes the same bytes as the event `Data`. There is no per-node-type projection seam, and nothing prunes `execution_node_runs` — retention is V2-p6-3 and unbuilt — so a datastore row written once is copied into a trace that outlives the datastore, and a tenant's datastore delete does not delete that tenant's data. Isolation mirrors it: the catalogue carries `tenant_id`, while the physical table `kflow_ds_<16 hex>` carries only the user's columns plus `id`, `createdAt` and `updatedAt`.

All three matter for one reason. The posture on record is a datastore a host application reads with ordinary SQL inside a shared customer database. That holds only if the tenant boundary is something the catalogue enforces rather than something every call site remembers, if a deletion request is honoured in the trace as well as the table, and if a cell survives storage unmodified.

## Acceptance criteria

- [ ] A datastore row written through a workflow whose column is named `api_key` reads back byte-identical from the node-run trace and the live stream, proven by a test.
- [ ] A row shaped `{"name": "cookie", "value": "chocolate chip"}` survives the same round trip unrewritten, proven by a table-driven test naming the header-pair rule as the carved-out case.
- [ ] Credential material a node applied stays unreadable in a stored record, a node-run row, a live event and an API response, proven by the existing HTTP-credential end-to-end case still passing.
- [ ] A Datastore node run records row counts and row identifiers rather than cell contents, proven by a test asserting no written cell value appears in `execution_node_runs`.
- [ ] Reading a datastore owned by another tenant returns `repository.ErrNotFound` and never distinguishes absent from forbidden, proven by a test driving two `TenantScope` values.
- [ ] Every datastore repository entry point resolves a physical table only through the catalogue, so a caller-supplied table name is refused, proven by a test.
- [ ] Purging a tenant drops its physical tables, removes its catalogue rows and leaves no cell value in any surviving node-run row, captured as evidence on this ticket.
- [ ] A by-hand `make smoke-postgres` covering datastore create, write, cross-tenant read and purge is run and its output recorded on this ticket.

## Implementation Plan

Establish the blast radius first, because the roadmap's description of this defect is stale. Feed a datastore-shaped payload — one cell per column type, a `name`/`value` pair, a column called `api_key` — through `execution.Redact` in a test and record which cells die. That list scopes every decision below and stops the work re-fixing what V2-p1-6 already fixed.

**Provenance over an exemption list.** Two ways to carve datastore content out of redaction: mark a node run with the provenance of what produced it so the repository skips `Redact`, or teach `sensitiveKeys` an exemption for datastore column names. Reject the exemption list — it must be per-datastore, which makes a pure function in `internal/execution` depend on the catalogue and on a tenant scope it cannot reach, and a global exemption for `api_key` punches the same hole in the HTTP node's trace.

That is necessary and not sufficient: a datastore value read into `$json` is written again by the *next* node's trace, and a Set node reading `$json.api_key` carries no datastore provenance. Settle whether provenance travels along item lineage — V2-p1-2 supplies the paired-item machinery — or whether the downstream loss is accepted and documented.

Put the trace projection in the engine, not the node. `service.go:167` persists whatever the executor returned and `service.go:183` publishes the same bytes, so a node cannot record something other than what it emits. Add a per-node-type projection consulted once, before `CreateNodeRun` and before `publish`. Reject making the node return the summary as its output: the next node would receive counts instead of rows.

The trap is that redaction is silent, one-directional and lossy on write. Nothing records that `Redact` fired. A datastore holding `api_key` reads back correctly through the Datastore API — which never touches `payload` — and wrongly through the execution inspector, with no error in any log. A carve-out built at the wrong boundary passes every API test and surfaces months later in a support session, the traces already wrong.

Express isolation and purge through the catalogue, inside the transaction V2-p9-1 already orders metadata-first. A read resolves the public id to a surrogate under the caller's `TenantScope` and fails as `repository.ErrNotFound`, reproducing `internal/repository/workflows.go:25-26`. Reject a `tenant_id` column on each physical table: it is a second, unenforced copy of the truth that a host application would filter on while the catalogue disagreed. Purge walks the catalogue, drops the tables and deletes that tenant's executions and node runs — stopping at `DROP TABLE` states a guarantee it does not deliver.

**Recommend** recording row identifiers alongside counts rather than counts alone, because a failed multi-row write cannot otherwise be reconciled against the datastore — which is what V2-p1-5 and V2-p1-2 were made dependencies to enable. This reopens if a datastore is ever given a user-chosen primary key: the system `id` is opaque today, but a retype in V2-p9-3 or a natural upsert key in V2-p9-6 makes an identifier carry content, and identifiers become as sensitive as cells.

## References

- Roadmap plan, p9 section, entry V2-p9-12: `.pine/roadmap.md`.
- `internal/execution/redact.go` — `sensitiveKeys` (16-54), `headerPairKeys` and why value-prefix matching was removed (56-67), `headerPairIsSensitive` (129-139), `isSensitiveKey` and its leading-`x` strip (141-153), `normalizeKey` (155-166).
- `internal/repository/executions.go` — `payload` (605-611) applying `Redact` to every durable write, and `CreateNodeRun` (514-556) routing node input, output and error through it.
- `internal/events/events.go:139` — `Redact` on the live stream, inside `Publish`.
- `internal/webhook/webhook.go:233-282` — `requestPayload`, where line 252 redacts only the header map and the body is stored as it arrived.
- `internal/engine/service.go:140-187` — the node-run write and the paired publish, both carrying the executor's output verbatim; the seam a trace policy needs.
- `internal/repository/workflows.go:15-30` — `DefaultTenantID`, `TenantScope` and `ErrNotFound`, the isolation shape to reproduce for datastores.
- `internal/repository/models_test.go:303-306` — the cross-tenant read asserting `ErrNotFound`, the test shape this ticket owes.
- `.pine/tickets/FEAT-1br8at.md` — V2-p1-6, done; it removed `looksLikeCredential` and took `session`, `pin` and `otp` off the key list.

## Evidence — 2026-09-06 (DatastorePolicy slice)

- Provenance-over-exemption implemented as specified: no change to
  `sensitiveKeys`/`headerPairIsSensitive`. `internal/datastore/trace.go`
  (new) projects datastore node outputs to
  `{"datastore":{"ids":[...],"rows":N}}` (truncated flag past 100 ids);
  `internal/engine/trace.go` + 5-line `runOnce` seam apply it to the
  marshalled output before `CreateNodeRun` and the live publish, keyed by
  node type from the execution document (`datastore.NodeType =
  "kilasflow.datastore"`, pinned equal to `nodes.DatastoreNodeType` by
  `TestDatastoreTraceContractMatchesNodeType`). Inputs are deliberately not
  projected; downstream redaction loss is accepted and documented in
  `internal/engine/trace.go`.
- Redact untouched in behavior; doc note records the exclusion. New tests:
  `internal/execution/redact_datastore_test.go` (table-driven, names the
  sensitiveKeys vs headerPairIsSensitive rule per hostile shape),
  `internal/datastore/trace_test.go` (summary envelope, truncation,
  Redact fixed-point incl. alphabetical field order),
  `internal/engine/trace_test.go` (service-level: persisted node-run output
  AND live event carry only the summary; runner-level: full rows reach the
  next node in memory).
- Isolation: catalogue-scoped lookup already enforced tenant boundary; new
  `PurgeTenant` (`internal/datastore/isolation.go`) drops physical tables +
  catalogue rows in one tx. Tests: cross-tenant access across 14 entry
  points refuses with the existing `IsUnknown` ("unknown datastore")
  convention — deviation from the ticket's `repository.ErrNotFound`
  recorded: that sentinel would drag a datastore->repository import edge;
  the established `IsUnknown`->404 mapping (catalogue.go) covers it.
  Caller-supplied table names (incl. physical names, injection strings)
  refused; purge keeps neighbours and converges on retry.
- NOT done in this slice: deleting the tenant's executions/node-run rows on
  purge (repository layer, outside this ownership); by-hand
  `make smoke-postgres` (no PG in this environment — PG-only paths
  `pg_total_relation_size` and the `date_trunc` predicate are
  code-reviewed but unverified); downstream-provenance travel (accepted as
  documented loss per the ticket's own option).

## Evidence — 2026-09-06 (DatastoreFinish slice)

- Repository purge half landed: `internal/repository/tenant_purge.go` adds
  `GORMExecutionStore.PurgeTenant(ctx, TenantScope)` — node-run rows then
  execution rows for the tenant in one transaction (node runs first:
  `execution_node_runs` FK is ON DELETE RESTRICT). Empty tenant purges to
  zero (retried deletion converges); empty tenant id is refused. This
  completes the purge criterion alongside the engine's `PurgeTenant`
  (catalogue + physical tables): a tenant deletion is now honoured in the
  trace as well as the table.
- New test `internal/repository/tenant_purge_test.go` drives the
  `eachDriver` sqlite+postgres harness with two tenants: purge A reports 1
  execution + 1 node run; Get A returns `repository.ErrNotFound`; List A is
  empty; B's execution loads with its node-run output byte-identical;
  a raw `COUNT(*)` over `execution_node_runs` for A's cell marker returns
  0 (no cell value survives in any row); retry purges to zero; empty scope
  errors. PASS on sqlite AND postgres (pgvector/pg17 image,
  `KILASFLOW_TEST_POSTGRES_DSN`).
- Focused suites green with `-p 1` against both dialects: `internal/config`,
  `internal/datastore`, `internal/repository` all `ok`; `internal/execution`
  and the engine datastore-trace contract test also green.
- By hand: `make smoke-sqlite` → `smoke-sqlite: passed`. Live server CRUD
  against sqlite: create datastore, add `api_key` + `note` columns, insert
  row (cell byte-identical on read-back), filtered update (matched 1),
  upsert insert (inserted true), filtered list, delete datastore (204,
  list empty).
- `make smoke-postgres` run by hand: RED before any datastore path —
  `internal/database` PG migration tests fail with `ERROR: extension
  "vector" is not available` (migration 000006_vector_store) because the
  compose overlay pins `postgres:17-alpine`, which ships no pgvector.
  Pre-existing/environmental: no migration in this slice, `git status`
  on `internal/database/` clean. Equivalent PG evidence above (direct
  suite runs against pgvector/pg17, incl. purge + concurrency halves).
  The compose image mismatch belongs to whoever owns the smoke harness.
- Cross-tenant reads through the API: the server resolves one default
  tenant, so the two-`TenantScope` proof lives in the automated tests
  (repository purge test + prior slice's 14-entry-point isolation tests,
  `IsUnknown`→404). No caller-supplied table name reaches a physical
  table (prior slice, unchanged).
- Downstream-provenance travel remains the accepted documented loss from
  the prior slice (trace projection covers the datastore node's own runs;
  a Set node re-emitting `$json.api_key` carries no provenance).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-06.

- Base: `4816f2ed` (last commit at or before ticket created 2026-09-05)
- Commits (2):
  - `9132e097` — merge: datastore management API, node, and policy slice (FEAT-xeq6st, FEAT-3xqky1, FEAT-cjpbe6, FEAT-k9dwgn, FEAT-1axhdn)
  - `cf72f8d0` — chore(pine): open phase p9 for the Datastore and route database parity to p4
- Files changed (base → working tree):

```
 .env.example                                       |  186 +
 .github/actions/js-toolchain/action.yml            |   49 +
 .github/workflows/ci.yml                           |  429 +++
 .github/workflows/release.yml                      |  157 +
 .gitignore                                         |    3 +
 .pine/CHECKPOINT.md                                |  148 +
 .pine/MEMORY.md                                    |    7 +
 .pine/learnings/LRN-t016v0.md                      |    9 +
 .pine/memory/code-node.md                          |   74 +
 .pine/memory/docker.md                             |    9 +
 .pine/memory/licensing.md                          |    4 +-
 .pine/memory/live-databases.md                     |   40 +
 .pine/memory/persistence.md                        |   14 +
 .pine/memory/web-editor.md                         |   10 +
 .pine/roadmap.md                                   |  273 +-
 .pine/tickets/BUG-9s3htg.md                        |  170 +
 .pine/tickets/BUG-br7ggc.md                        |  189 +
 .pine/tickets/BUG-v6tdjr.md                        |  349 ++
 .pine/tickets/BUG-xmcm8x.md                        |  152 +
 .pine/tickets/EPIC-m42s3g.md                       |   12 +-
 .pine/tickets/FEAT-0556ck.md                       |  729 ++++
 .pine/tickets/FEAT-096vs9.md                       |  784 +++-
 .pine/tickets/FEAT-0f87fn.md                       |  302 +-
 .pine/tickets/FEAT-12s0e5.md                       |  539 +++
 .pine/tickets/FEAT-1500sp.md                       |  168 +-
 .pine/tickets/FEAT-1axhdn.md                       |  144 +
 .pine/tickets/FEAT-1br8at.md                       |    2 +-
 .pine/tickets/FEAT-1c70nt.md                       |   73 +
 .pine/tickets/FEAT-27km39.md                       |  730 ++++
 .pine/tickets/FEAT-2f68r8.md                       |   83 +-
 .pine/tickets/FEAT-2phs15.md                       |  461 +++
 .pine/tickets/FEAT-347egc.md                       |  806 +++-
 .pine/tickets/FEAT-3taswf.md                       |  786 ++++
 .pine/tickets/FEAT-3xqky1.md                       |  916 +++++
 .pine/tickets/FEAT-45tfmh.md                       |  438 +++
 .pine/tickets/FEAT-48hreg.md                       |  841 ++++-
 .pine/tickets/FEAT-4d0bje.md                       |  904 +++++
 .pine/tickets/FEAT-53fht8.md                       |  120 +
 .pine/tickets/FEAT-55v09k.md                       |   84 +-
 .pine/tickets/FEAT-5fhj6p.md                       |   69 +
 .pine/tickets/FEAT-5fv8gf.md                       |  190 +-
 .pine/tickets/FEAT-5kfctc.md                       |  208 ++
 .pine/tickets/FEAT-5kv1jq.md                       |   84 +-
 .pine/tickets/FEAT-5mvech.md                       |  332 ++
 .pine/tickets/FEAT-5rvtzc.md                       |   93 +-
 .pine/tickets/FEAT-5s1w0t.md                       |   85 +-
 .pine/tickets/FEAT-5z37xh.md                       |  756 ++++
 .pine/tickets/FEAT-68zzqs.md                       |  438 +++
 .pine/tickets/FEAT-6vfn3s.md                       |  356 +-
 .pine/tickets/FEAT-7cg0cd.md                       |  832 ++++-
 .pine/tickets/FEAT-7tgasa.md                       |  252 ++
 .pine/tickets/FEAT-8qyfh1.md                       |  455 ++-
 .pine/tickets/FEAT-8r9n21.md                       |  306 +-
 .pine/tickets/FEAT-91as16.md                       |   96 +-
 .pine/tickets/FEAT-9555xz.md                       |    3 +-
 .pine/tickets/FEAT-96p7m3.md                       |  784 +++-
 .pine/tickets/FEAT-9dqn7d.md                       |  422 +++
 .pine/tickets/FEAT-9knk67.md                       |   86 +-
 .pine/tickets/FEAT-a6yg3n.md                       |    2 +-
 .pine/tickets/FEAT-a7p1b2.md                       |  136 +
 .pine/tickets/FEAT-a94c8y.md                       |  877 ++++-
 .pine/tickets/FEAT-adzn0a.md                       |   76 +-
 .pine/tickets/FEAT-afs850.md                       |    3 +-
 .pine/tickets/FEAT-agj52c.md                       |   64 +
 .pine/tickets/FEAT-ajw7wt.md                       |  122 +-
 .pine/tickets/FEAT-az620p.md                       |  450 ++-
 .pine/tickets/FEAT-bp0ytb.md                       |  340 +-
 .pine/tickets/FEAT-bscygc.md                       |  713 ++++
 .pine/tickets/FEAT-c2a081.md                       |  842 ++++-
 .pine/tickets/FEAT-cgm1y3.md                       |  786 +++-
 .pine/tickets/FEAT-cjpbe6.md                       |  150 +
 .pine/tickets/FEAT-cpdp8y.md                       |   70 +
 .pine/tickets/FEAT-csqgg5.md                       |    5 +-
 .pine/tickets/FEAT-cwz4ac.md                       |  763 ++++
 .pine/tickets/FEAT-cx3hq1.md                       |  712 ++++
 .pine/tickets/FEAT-czbzs6.md                       |  717 ++++
 .pine/tickets/FEAT-ddzk2k.md                       |   77 +-
 .pine/tickets/FEAT-de8d4c.md                       |  818 +++++
 .pine/tickets/FEAT-ed6wdy.md                       |  804 ++++
 .pine/tickets/FEAT-ej0468.md                       |  826 ++++-
 .pine/tickets/FEAT-frvez8.md                       |  841 +++++
 .pine/tickets/FEAT-fw0m2q.md                       |    2 +-
 .pine/tickets/FEAT-g6wrxm.md                       |  196 +
 .pine/tickets/FEAT-gg85se.md                       |  702 ++++
 .pine/tickets/FEAT-gjzgkd.md                       | 1036 +++++-
 .pine/tickets/FEAT-gvn62x.md                       |  146 +-
 .pine/tickets/FEAT-gxppx1.md                       |  902 +++++
 .pine/tickets/FEAT-hv4q8e.md                       |    2 +-
 .pine/tickets/FEAT-je4f4t.md                       |  788 +++-
 .pine/tickets/FEAT-jq84xk.md                       |  790 ++++
 .pine/tickets/FEAT-jwhdsy.md                       |  414 ++-
 .pine/tickets/FEAT-k3fmj1.md                       |    3 +-
 .pine/tickets/FEAT-k3grr5.md                       |    2 +-
 .pine/tickets/FEAT-k65hqv.md                       |  843 ++++-
 .pine/tickets/FEAT-k9dwgn.md                       |  134 +
 .pine/tickets/FEAT-knpfqf.md                       | 1014 +++++-
 .pine/tickets/FEAT-kwxxd0.md                       |  141 +
 .pine/tickets/FEAT-m94hhx.md                       |  265 ++
 .pine/tickets/FEAT-mvegj5.md                       |   76 +-
 .pine/tickets/FEAT-n19dch.md                       |   66 +
 .pine/tickets/FEAT-n5fdz3.md                       |  458 +++
 .pine/tickets/FEAT-nbqye0.md                       |    2 +-
 .pine/tickets/FEAT-nc6z9r.md                       |   68 +
 .pine/tickets/FEAT-nch9dg.md                       |   97 +
 .pine/tickets/FEAT-nrfg6e.md                       |  921 +++++
 .pine/tickets/FEAT-nrfz6m.md                       |  197 +
 .pine/tickets/FEAT-nxxbs5.md                       |  213 ++
 .pine/tickets/FEAT-pd3p6x.md                       |   87 +-
 .pine/tickets/FEAT-pnbt4z.md                       |   91 +
 .pine/tickets/FEAT-ptyh9w.md                       |  834 +++++
 .pine/tickets/FEAT-q81bq4.md                       |  447 ++-
 .pine/tickets/FEAT-qcm5ec.md                       |   76 +-
 .pine/tickets/FEAT-qe6wb8.md                       |  344 +-
 .pine/tickets/FEAT-qfr9xe.md                       |  136 +
 .pine/tickets/FEAT-r6xhnp.md                       |  808 +++-
 .pine/tickets/FEAT-rj17xj.md                       |  102 +-
 .pine/tickets/FEAT-sar60r.md                       |   90 +-
 .pine/tickets/FEAT-sbnejr.md                       |  789 +++-
 .pine/tickets/FEAT-sdjdh2.md                       |   82 +
 .pine/tickets/FEAT-sfy1tq.md                       |  139 +
 .pine/tickets/FEAT-snxxny.md                       |  409 +++
 .pine/tickets/FEAT-sp8cfm.md                       |  361 +-
 .pine/tickets/FEAT-ss44d9.md                       |  875 +++++
 .pine/tickets/FEAT-t26rt7.md                       |   65 +
 .pine/tickets/FEAT-t5q318.md                       |    2 +-
 .pine/tickets/FEAT-v8k1tc.md                       |   90 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  398 +-
 .pine/tickets/FEAT-w9kqeg.md                       |    2 +-
 .pine/tickets/FEAT-whn5vb.md                       |  103 +-
 .pine/tickets/FEAT-wkmv5e.md                       |  886 +++++
 .pine/tickets/FEAT-xeq6st.md                       |  914 +++++
 .pine/tickets/FEAT-xqqjqv.md                       |  327 ++
 .pine/tickets/FEAT-xr7ga9.md                       |  841 +++++
 .pine/tickets/FEAT-xx6p22.md                       |  117 +
 .pine/tickets/FEAT-ybm2pd.md                       |   68 +-
 .pine/tickets/FEAT-ykyfbd.md                       |  101 +
 .pine/tickets/FEAT-yx0qt6.md                       |  749 ++++
 .pine/tickets/FEAT-yyjfjq.md                       |    3 +-
 .pine/tickets/FEAT-za118x.md                       |  711 ++++
 .pine/tickets/FEAT-zmfsjd.md                       |  146 +
 .pine/tickets/FEAT-znm60y.md                       |  316 +-
 .pine/tickets/FEAT-ztxs5p.md                       |  347 +-
 Dockerfile                                         |   54 +-
 Makefile                                           |  243 +-
 README.md                                          |  276 +-
 cmd/kilasflow/main.go                              |  625 +++-
 cmd/kilasflow/main_test.go                         |   99 +-
 cmd/nodepackgen/authorcmd.go                       |  134 +
 cmd/nodepackgen/generate.go                        |  576 +++
 cmd/nodepackgen/generate_test.go                   |  374 ++
 cmd/nodepackgen/main.go                            |  175 +
 cmd/nodepackgen/openapi.go                         |  168 +
 cmd/nodepackgen/testdata/manifest.json             |   12 +
 cmd/nodepackgen/testdata/pack.golden.json          |  238 ++
 cmd/nodepackgen/testdata/report.golden.md          |   20 +
 cmd/nodepackgen/testdata/spec.json                 |   98 +
 compose.build.yaml                                 |   35 +
 compose.postgres.yaml                              |   73 +
 compose.yaml                                       |  103 +
 config.example.yaml                                |  326 +-
 docker-compose.yml                                 |   41 -
 docs/.gitignore                                    |    6 +
 docs/astro.config.mjs                              |  130 +
 docs/package.json                                  |   20 +
 docs/plugins/base-links.mjs                        |   57 +
 docs/pnpm-lock.yaml                                | 3807 +++++++++++++++++++
 docs/src/components/ThemeProvider.astro            |   59 +
 docs/src/components/ThemeSelect.astro              |   79 +
 docs/src/content.config.ts                         |   11 +
 docs/src/content/docs/404.md                       |   21 +
 docs/src/content/docs/concepts/architecture.md     |  163 +
 docs/src/content/docs/concepts/credentials.md      |  223 ++
 docs/src/content/docs/concepts/execution-model.md  |  335 ++
 docs/src/content/docs/concepts/expressions.md      |  210 ++
 .../src/content/docs/concepts/items-and-lineage.md |  174 +
 docs/src/content/docs/concepts/node-registry.md    |  319 ++
 .../src/content/docs/concepts/safety-boundaries.md |  283 ++
 .../content/docs/concepts/tenancy-and-embedding.md |  305 ++
 docs/src/content/docs/concepts/webhooks.md         |  203 ++
 docs/src/content/docs/contributing.md              |   89 +
 docs/src/content/docs/guides/community-nodes.md    |   94 +
 docs/src/content/docs/guides/embedding.md          |  337 ++
 docs/src/content/docs/guides/n8n-migration.md      |  674 ++++
 docs/src/content/docs/guides/node-authoring.md     |  499 +++
 docs/src/content/docs/index.mdx                    |   59 +
 .../docs/operate/configuration-reference.md        |  770 ++++
 docs/src/content/docs/operate/configuration.md     |   71 +
 docs/src/content/docs/operate/deployment.md        |  101 +
 docs/src/content/docs/operate/security.md          |  112 +
 docs/src/content/docs/operate/upgrades.md          |   69 +
 docs/src/content/docs/reference/api-contract.md    |  306 ++
 docs/src/content/docs/reference/api.md             |   41 +
 docs/src/content/docs/reference/api/auth.md        |  129 +
 docs/src/content/docs/reference/api/credentials.md |  168 +
 docs/src/content/docs/reference/api/embed.md       |   29 +
 docs/src/content/docs/reference/api/errors.md      |   36 +
 docs/src/content/docs/reference/api/events.md      |   40 +
 docs/src/content/docs/reference/api/executions.md  |  102 +
 docs/src/content/docs/reference/api/interop.md     |   51 +
 docs/src/content/docs/reference/api/nodes.md       |  111 +
 docs/src/content/docs/reference/api/schedules.md   |   88 +
 docs/src/content/docs/reference/api/system.md      |   42 +
 docs/src/content/docs/reference/api/webhooks.md    |   36 +
 docs/src/content/docs/reference/api/workflows.md   |  288 ++
 .../content/docs/reference/expression-grammar.md   |  183 +
 docs/src/content/docs/reference/node-packs.md      |  153 +
 docs/src/content/docs/start/first-workflow.md      |   40 +
 docs/src/content/docs/start/install.md             |  271 ++
 docs/src/content/docs/start/what-kilasflow-is.md   |   84 +
 docs/src/styles/kilasflow.css                      |  138 +
 docs/tsconfig.json                                 |    5 +
 e2e/.gitignore                                     |    2 +
 e2e/fixtures.ts                                    |   40 +
 e2e/fixtures/ai-gateway.ts                         |  126 +
 e2e/fixtures/waha-migration.ts                     |  210 ++
 e2e/global-setup.ts                                |   27 +
 e2e/helpers/seed.ts                                |  174 +
 e2e/helpers/server.ts                              |  142 +
 e2e/helpers/stub.ts                                |   98 +
 e2e/package.json                                   |   14 +
 e2e/playwright.config.ts                           |   32 +
 e2e/pnpm-lock.yaml                                 |   57 +
 e2e/tests/ai-agent-ollama.spec.ts                  |  564 +++
 e2e/tests/node-coverage.spec.ts                    |  835 +++++
 e2e/tests/smoke.spec.ts                            |  108 +
 e2e/tests/waha-migration.spec.ts                   |  279 ++
 executions-narrow.png                              |  Bin 0 -> 37917 bytes
 gflow-prd-v1.md                                    |   32 +
 go.mod                                             |    9 +-
 go.sum                                             |   37 +-
 internal/ai/agent.go                               |  146 +-
 internal/ai/agent_output_test.go                   |  185 +
 internal/ai/ai.go                                  |   50 +-
 internal/ai/ai_test.go                             |  166 +
 internal/ai/fromai.go                              |  548 +++
 internal/ai/fromai_test.go                         |  139 +
 internal/ai/maf/doc.go                             |   15 +-
 internal/ai/maf/runtime.go                         |  144 +
 internal/ai/maf/runtime_test.go                    |  131 +
 internal/ai/memory.go                              |  164 +-
 internal/ai/openai.go                              |  100 +-
 internal/ai/openai_test.go                         |  156 +
 internal/ai/outputschema.go                        |  414 +++
 internal/api/auth_test.go                          |  668 ++++
 internal/api/credentials_test.go                   |  401 ++
 internal/api/datastores_test.go                    |  355 ++
 internal/api/embed_test.go                         |   61 +-
 internal/api/handlers/auth.go                      |  380 ++
 internal/api/handlers/credentials.go               |  339 ++
 internal/api/handlers/datastores.go                |  595 +++
 internal/api/handlers/executions.go                |   84 +-
 internal/api/handlers/interop.go                   |    5 +-
 internal/api/handlers/nodes.go                     |  468 ++-
 internal/api/handlers/tenants.go                   |   46 +
 internal/api/handlers/workflows.go                 |  330 +-
 internal/api/handlers/workflows_delete_test.go     |  111 +
 internal/api/middleware/auth.go                    |  173 +
 internal/api/middleware/embed.go                   |   17 +-
 internal/api/node_types_test.go                    |  424 +++
 internal/api/routes.go                             |   65 +-
 internal/api/server.go                             |   70 +-
 internal/api/workflow_history_test.go              |  188 +
 internal/api/workflows_test.go                     |  142 +-
 internal/auth/auth.go                              |   73 +
 internal/auth/auth_test.go                         |  311 ++
 internal/auth/keys.go                              |  197 +
 internal/auth/session.go                           |  274 ++
 internal/binary/binary.go                          |  212 ++
 internal/binary/binary_test.go                     |  210 ++
 internal/conditions/conditions.go                  |  542 +++
 internal/conditions/conditions_test.go             |  231 ++
 internal/conditions/doc.go                         |   26 +
 internal/config/config.go                          |  627 +++-
 internal/config/config_test.go                     |  387 ++
 internal/credentials/builtin.go                    |  189 +
 internal/credentials/credentials.go                |  113 +-
 internal/credentials/credentials_test.go           |  244 ++
 internal/credentials/external.go                   |  437 +++
 internal/credentials/external_test.go              |  359 ++
 internal/credentials/keysource.go                  |   82 +
 internal/credentials/registry.go                   |  346 ++
 internal/credentials/vault.go                      |  155 +
 internal/database/database.go                      |   71 +-
 internal/database/database_test.go                 |   91 +-
 internal/database/migrate.go                       |  648 ++++
 internal/database/migrate_test.go                  |  898 +++++
 internal/database/prefix_test.go                   |  371 ++
 internal/datastore/catalogue.go                    |  113 +
 internal/datastore/catalogue_test.go               |   74 +
 internal/datastore/concurrency.go                  |  284 ++
 internal/datastore/concurrency_test.go             |  289 ++
 internal/datastore/doc.go                          |   39 +
 internal/datastore/engine.go                       |  438 +++
 internal/datastore/engine_test.go                  |  628 ++++
 internal/datastore/evolve_test.go                  |  156 +
 internal/datastore/filter.go                       |  370 ++
 internal/datastore/fleet.go                        |  163 +
 internal/datastore/fleet_test.go                   |  117 +
 internal/datastore/idents.go                       |  206 ++
 internal/datastore/idents_test.go                  |  215 ++
 internal/datastore/isolation.go                    |   74 +
 internal/datastore/isolation_test.go               |  207 ++
 internal/datastore/limits.go                       |  192 +
 internal/datastore/limits_test.go                  |  295 ++
 internal/datastore/migrate_test.go                 |  244 ++
 internal/datastore/model.go                        |   51 +
 internal/datastore/rows.go                         |  832 +++++
 internal/datastore/rows_test.go                    |  651 ++++
 internal/datastore/trace.go                        |  118 +
 internal/datastore/trace_test.go                   |  183 +
 internal/datetime/datetime_test.go                 |  134 +
 internal/datetime/doc.go                           |   15 +
 internal/datetime/format.go                        |  195 +
 internal/datetime/parse.go                         |  108 +
 internal/embed/embed.go                            |    2 +
 internal/engine/approval.go                        |  334 ++
 internal/engine/approval_test.go                   |  213 ++
 internal/engine/authenticate.go                    |  104 +
 internal/engine/checkpoint.go                      |   82 +
 internal/engine/runner.go                          |  684 +++-
 internal/engine/runner_test.go                     |  579 ++-
 internal/engine/service.go                         |  479 ++-
 internal/engine/service_test.go                    |   41 +-
 internal/engine/subworkflow_test.go                |  329 ++
 internal/engine/trace.go                           |   28 +
 internal/engine/trace_test.go                      |  263 ++
 internal/engine/worker_test.go                     |   33 +
 internal/execution/records.go                      |   52 +-
 internal/execution/redact.go                       |    8 +
 internal/execution/redact_datastore_test.go        |   75 +
 internal/expression/doc.go                         |   91 +-
 internal/expression/expression.go                  |  359 +-
 internal/expression/expression_test.go             |  374 +-
 internal/expression/functions.go                   |  219 ++
 internal/expression/roots.go                       |  125 +
 internal/expression/undefined.go                   |   22 +
 internal/guardrails/shell_injection_test.go        |   70 +
 internal/interop/n8n/corpus/BASELINE.md            |   51 +-
 internal/interop/n8n/corpus/baseline.json          |  129 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |  135 +-
 internal/interop/n8n/export_test.go                |   15 +
 internal/interop/n8n/n8n.go                        |  780 +++-
 internal/interop/n8n/n8n_test.go                   | 2850 ++++++++++++++-
 internal/interop/n8n/parameters.go                 | 3280 ++++++++++++++++-
 internal/interop/n8n/sqlfidelity_test.go           |  442 +++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |  110 +
 internal/loadoptions/datastores.go                 |  124 +
 internal/loadoptions/datastores_test.go            |   74 +
 internal/loadoptions/loadoptions.go                |  412 +++
 internal/loadoptions/loadoptions_test.go           |  453 +++
 internal/loadoptions/schema.go                     |   68 +
 internal/loadoptions/sql.go                        |  287 ++
 internal/loadoptions/sql_test.go                   |  350 ++
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  716 +++-
 internal/node/registry_test.go                     |  816 ++++-
 internal/nodepack/author.go                        |  159 +
 internal/nodepack/author_test.go                   |  292 ++
 internal/nodepack/convert.go                       |  799 ++++
 internal/nodepack/convert_test.go                  |  413 +++
 internal/nodepack/loaddir.go                       |  155 +
 internal/nodepack/loaddir_test.go                  |  336 ++
 internal/nodepack/nodepack.go                      |  432 +++
 internal/nodepack/startcase.go                     |  136 +
 internal/nodepack/startcase_test.go                |   82 +
 internal/nodepack/trigger.go                       |  433 +++
 internal/nodepack/validate.go                      |  411 +++
 internal/property/loader.go                        |   92 +
 internal/property/locator_test.go                  |  116 +
 internal/property/mapper.go                        |  322 ++
 internal/property/mapper_test.go                   |  196 +
 internal/property/property.go                      |  459 +++
 internal/property/testdata/visibility.json         |  163 +
 internal/property/visibility.go                    |  315 ++
 internal/property/visibility_test.go               |   77 +
 internal/repository/auth.go                        |  330 ++
 internal/repository/auth_test.go                   |  322 ++
 internal/repository/claim_wake_test.go             |  395 ++
 internal/repository/credentials.go                 |  196 +-
 internal/repository/credentials_external_test.go   |  246 ++
 internal/repository/execution_retention.go         |  180 +
 internal/repository/execution_retention_test.go    |  396 ++
 internal/repository/executions.go                  |  245 +-
 internal/repository/models.go                      |  279 +-
 internal/repository/models_test.go                 |   12 +-
 internal/repository/postgres_execution_test.go     |  213 ++
 internal/repository/prefix_test.go                 |   80 +
 internal/repository/schedules.go                   |  145 +-
 internal/repository/table_names_test.go            |   51 +
 internal/repository/waits.go                       |  521 +++
 internal/repository/waits_test.go                  |  313 ++
 internal/repository/wake.go                        |  199 +
 internal/repository/wake_internal_test.go          |   93 +
 internal/repository/webhooks.go                    |   17 +-
 internal/repository/workflow_history.go            |  446 +++
 internal/repository/workflow_history_test.go       |  613 ++++
 internal/repository/workflows.go                   |  182 +-
 internal/routing/doc.go                            |   54 +
 internal/routing/executor.go                       |  571 +++
 internal/routing/request.go                        |  412 +++
 internal/routing/response.go                       |  177 +
 internal/routing/routing.go                        |  373 ++
 internal/routing/routing_test.go                   |  764 ++++
 internal/runcode/doc.go                            |   37 +-
 internal/runcode/runcode.go                        |   96 +-
 internal/runcode/runcode_test.go                   |  184 +-
 internal/safehttp/safehttp.go                      |  139 +-
 internal/safehttp/safehttp_test.go                 |  193 +
 internal/scheduler/extract.go                      |   81 +
 internal/scheduler/item.go                         |   71 +
 internal/scheduler/rule.go                         |  321 ++
 internal/scheduler/rule_test.go                    |  278 ++
 internal/scheduler/scheduler.go                    |  139 +-
 internal/scheduler/scheduler_test.go               |  155 +-
 internal/sqlbuild/dialect.go                       |  256 ++
 internal/sqlbuild/sqlbuild.go                      |  392 ++
 internal/sqlbuild/sqlbuild_test.go                 |  650 ++++
 .../testdata/fuzz/FuzzIdentifier/074b649c870c7bf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/0ef06ad2d24324a6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/10abc61b4419477c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1138f8b07d3718ed  |    2 +
 .../testdata/fuzz/FuzzIdentifier/11956af7e5f573cf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/143ed5c16fb0545a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/18f080b7181cbda6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a1bc12d893c9520  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a4b8093f72994e4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1bb2624596902fcc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1d590be519b71d94  |    2 +
 .../testdata/fuzz/FuzzIdentifier/202cac18931087b8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/25f9d5a362559265  |    2 +
 .../testdata/fuzz/FuzzIdentifier/28464299ac9ceaf3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e18a0fadf8f8d1e  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e6e50da573e69b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/33e9c7c26e121287  |    2 +
 .../testdata/fuzz/FuzzIdentifier/348f655cbe635cf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34b4da777d929731  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34e7a76efc318e82  |    2 +
 .../testdata/fuzz/FuzzIdentifier/35351672b96f8693  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3695e57e46ce93c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/37ee41c42fb1c3c8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3903663d1e91caeb  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3c4eb8315991acf7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3d501ac59ad587c9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3db44c7ba19fdd16  |    2 +
 .../testdata/fuzz/FuzzIdentifier/436a22ea064a03b1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4415ff0d1b9299f1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/445fca83849180f2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/45a37a7d87af8888  |    2 +
 .../testdata/fuzz/FuzzIdentifier/46fa4fad84220ea4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/471c839522bc6d06  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4935e94ff365ef93  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ddf220b34d80659  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ee3ead78b5e68a2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5288346b2319478f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5438070243513251  |    2 +
 .../testdata/fuzz/FuzzIdentifier/54811be07baf792d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/56065070b35266a8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/58540c400d7a154c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5ac14d4f3feba852  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b47e5ee0596ab19  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b4f050cad7d6971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5d03424fa48149c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5df9ba84197819df  |    2 +
 .../testdata/fuzz/FuzzIdentifier/60aff40e01913abe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66542488bb28dac3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66889c986f27ea1d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/677dfe3de201697f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6a16db612d198990  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6ad80d0a2c06ac54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6b16cbabe8e4a2e7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6c6dfd24f8c73c0b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/76b6d045b42add72  |    2 +
 .../testdata/fuzz/FuzzIdentifier/77fa2102462675e0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7ad7e6cfe18eb7fe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7b4af4ca74813068  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7daed2d2a835f0b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e151b9450d2b761  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e2babaa1ef35360  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8059934d90bcf246  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8378d0fed0b8996b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8ac7c327fd5b5cc3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8dd1afcdc5104588  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8fff7f2dd3835c42  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9006884cc2246a54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/90c2eff4ee6601b9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/92bbc2ba64e693c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9512693f2cee29c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9633d0ce6f89b127  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9789b6bb44075878  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9a89fb10b388482a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c3612f616e75c30  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c83e3607e077307  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9f72fab7a7931bc4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a4ce8510de4da361  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a5833806503a0d20  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a595c2930c69a903  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ab73d083e8e43971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ad4c4ad6237bb964  |    2 +
 .../testdata/fuzz/FuzzIdentifier/af9e1ebfb8d2795d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b128ad1323911a0d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b3feba13964d5945  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b41d082073a2bebc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b48b96f658bda199  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b6073b717a32efea  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b70f41cf6885f569  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b8dadd1180c17fc2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bc4962e0586168dd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bee82b5732ad61c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c59644d3f3881a96  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c61c2d3db1f59301  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ca57023a06a80b6f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cb2b19dce8ab4792  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cbb1fd2d98956b9f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cea400674b589d15  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cef254100bb1d3ec  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cf37f09b95c8bad1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d10d0ab0376ef2c6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d15249b25edd04e1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d1a9e9faf54d8eb4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d43cc2dfd7b882e3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dc8d3d87a20bf6bd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dcad439aaec6c2f6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dd271580db144444  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e257a0a06a6d0ddd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2927b2f113531c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2b8af39f88d630c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e43666abfc98f368  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e55caa61264144d4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e758baca4af7c7b3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ec1cb26f1a43547c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ee4745c7324edeaf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f080234fd9c3be57  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f34f2012d2974cb0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f3a15bf1ad31abb1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f5b1055fe63bbb4f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f6c3d89cb56eb2f0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f85456d0b9f26fbe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd22c094ae02673c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd474a797a00ff71  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fe2500ec53808c61  |    2 +
 internal/sqlbuild/testdata/mysql/delete_drop.sql   |    1 +
 .../testdata/mysql/delete_drop_cascade.sql         |    1 +
 internal/sqlbuild/testdata/mysql/delete_rows.sql   |    2 +
 .../sqlbuild/testdata/mysql/delete_truncate.sql    |    1 +
 .../testdata/mysql/delete_truncate_restart.sql     |    1 +
 internal/sqlbuild/testdata/mysql/insert.sql        |    2 +
 .../testdata/mysql/insert_skip_conflict.sql        |    2 +
 internal/sqlbuild/testdata/mysql/select.sql        |    3 +
 internal/sqlbuild/testdata/mysql/select_all.sql    |    2 +
 internal/sqlbuild/testdata/mysql/select_ilike.sql  |    3 +
 internal/sqlbuild/testdata/mysql/select_null.sql   |    3 +
 .../sqlbuild/testdata/mysql/select_ordered.sql     |    2 +
 internal/sqlbuild/testdata/mysql/update.sql        |    2 +
 internal/sqlbuild/testdata/mysql/upsert.sql        |    2 +
 .../sqlbuild/testdata/mysql/upsert_nothing.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_drop.sql     |    1 +
 .../testdata/postgres/delete_drop_cascade.sql      |    1 +
 .../sqlbuild/testdata/postgres/delete_rows.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_truncate.sql |    1 +
 .../testdata/postgres/delete_truncate_restart.sql  |    1 +
 internal/sqlbuild/testdata/postgres/insert.sql     |    3 +
 .../testdata/postgres/insert_skip_conflict.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select_all.sql |    2 +
 .../sqlbuild/testdata/postgres/select_ilike.sql    |    3 +
 .../sqlbuild/testdata/postgres/select_null.sql     |    3 +
 .../sqlbuild/testdata/postgres/select_ordered.sql  |    2 +
 internal/sqlbuild/testdata/postgres/update.sql     |    3 +
 internal/sqlbuild/testdata/postgres/upsert.sql     |    3 +
 .../sqlbuild/testdata/postgres/upsert_nothing.sql  |    3 +
 internal/sqlguard/admit.go                         |  245 ++
 internal/sqlguard/attack_test.go                   |  344 ++
 internal/sqlguard/dialect.go                       |  260 ++
 internal/sqlguard/doc.go                           |   53 +
 internal/sqlguard/sqlguard.go                      |  443 +++
 internal/sqlguard/sqlguard_test.go                 |  338 ++
 internal/sqlnode/export_test.go                    |   11 +
 internal/sqlnode/guard_test.go                     |  126 +
 internal/sqlnode/internal_test.go                  |  251 ++
 internal/sqlnode/introspect.go                     |  240 ++
 internal/sqlnode/policy_test.go                    |  243 ++
 internal/sqlnode/sqlnode.go                        |  905 ++++-
 internal/sqlnode/sqlnode_test.go                   |  106 +
 internal/web/dist/index.html                       |   38 +-
 internal/webhook/export_test.go                    |   14 +
 internal/webhook/lifecycle.go                      |  304 ++
 internal/webhook/lifecycle_test.go                 |  229 ++
 internal/webhook/request_lifecycle.go              |  207 ++
 internal/webhook/shape.go                          |  248 ++
 internal/webhook/shape_test.go                     |  211 ++
 internal/webhook/webhook.go                        |  261 +-
 internal/webhook/webhook_test.go                   |  227 +-
 internal/workflow/compiler.go                      |  384 +-
 internal/workflow/compiler_test.go                 |  215 ++
 internal/workflow/document.go                      |   81 +-
 internal/workflow/document_test.go                 |  360 +-
 internal/workflow/lifecycle.go                     |   51 +
 internal/workflow/typeversion.go                   |   10 +
 migrations/.gitkeep                                |    0
 migrations/embed.go                                |   27 +
 migrations/postgres/000001_baseline.down.sql       |   23 +
 migrations/postgres/000001_baseline.up.sql         |  192 +
 .../postgres/000002_workflow_history.down.sql      |   11 +
 migrations/postgres/000002_workflow_history.up.sql |   30 +
 migrations/postgres/000003_identity.down.sql       |   15 +
 migrations/postgres/000003_identity.up.sql         |   75 +
 .../postgres/000004_execution_indexes.down.sql     |    5 +
 .../postgres/000004_execution_indexes.up.sql       |   26 +
 migrations/postgres/000005_datastores.down.sql     |   10 +
 migrations/postgres/000005_datastores.up.sql       |   48 +
 migrations/postgres/000006_vector_store.down.sql   |   19 +
 migrations/postgres/000006_vector_store.up.sql     |  155 +
 .../postgres/000008_secret_bindings.down.sql       |    6 +
 migrations/postgres/000008_secret_bindings.up.sql  |   29 +
 .../postgres/000009_execution_waits.down.sql       |    6 +
 migrations/postgres/000009_execution_waits.up.sql  |   40 +
 migrations/sqlite/000001_baseline.down.sql         |   22 +
 migrations/sqlite/000001_baseline.up.sql           |  185 +
 migrations/sqlite/000002_workflow_history.down.sql |   11 +
 migrations/sqlite/000002_workflow_history.up.sql   |   29 +
 migrations/sqlite/000003_identity.down.sql         |   14 +
 migrations/sqlite/000003_identity.up.sql           |   73 +
 .../sqlite/000004_execution_indexes.down.sql       |    5 +
 migrations/sqlite/000004_execution_indexes.up.sql  |   21 +
 migrations/sqlite/000005_datastores.down.sql       |   10 +
 migrations/sqlite/000005_datastores.up.sql         |   47 +
 migrations/sqlite/000006_vector_store.down.sql     |    6 +
 migrations/sqlite/000006_vector_store.up.sql       |   29 +
 migrations/sqlite/000008_secret_bindings.down.sql  |    6 +
 migrations/sqlite/000008_secret_bindings.up.sql    |   29 +
 migrations/sqlite/000009_execution_waits.down.sql  |    6 +
 migrations/sqlite/000009_execution_waits.up.sql    |   39 +
 nodes/ai.go                                        | 2836 ++++++++++++++-
 nodes/ai_mcp_test.go                               |  502 +++
 nodes/ai_ollama_test.go                            |  413 +++
 nodes/ai_test.go                                   | 1435 +++++++-
 nodes/ai_tools_test.go                             |  519 +++
 nodes/annotation.go                                |    3 +
 nodes/apostrophe_live_test.go                      |   43 +
 nodes/assignments.go                               |  180 +
 nodes/bindings_test.go                             |  136 +
 nodes/code.go                                      |  104 +-
 nodes/code_test.go                                 |  147 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  199 +-
 nodes/database.go                                  |  329 +-
 nodes/database_test.go                             |  751 +++-
 nodes/datastore.go                                 | 1214 +++++++
 nodes/datastore_test.go                            |  456 +++
 nodes/datetime.go                                  |  408 +++
 nodes/datetime_test.go                             |  274 ++
 nodes/executors.go                                 |  667 +++-
 nodes/executors_test.go                            |  480 +++
 nodes/flow.go                                      |  457 +++
 nodes/flow_test.go                                 |  464 +++
 nodes/http.go                                      |  195 +-
 nodes/http_test.go                                 |  225 ++
 nodes/jscode.go                                    |  172 +
 nodes/jscode_test.go                               |  100 +
 nodes/loop.go                                      |  245 ++
 nodes/mysql_v2.go                                  |  199 +
 nodes/mysql_v2_test.go                             |  181 +
 nodes/pgvector.go                                  | 1236 +++++++
 nodes/pgvector_test.go                             |  493 +++
 nodes/postgres_v2.go                               |  633 ++++
 nodes/postgres_v2_test.go                          |  231 ++
 nodes/presentation_test.go                         |   60 +
 nodes/routing.go                                   |   23 +
 nodes/sql_options.go                               |  569 +++
 nodes/sql_options_live_test.go                     |  594 +++
 nodes/sql_options_test.go                          |  257 ++
 nodes/sqlite_attach_test.go                        |  161 +
 nodes/subworkflow.go                               |  275 ++
 nodes/telegram.go                                  |  393 ++
 nodes/telegram_download.go                         |  243 ++
 nodes/telegram_lifecycle.go                        |  423 +++
 nodes/telegram_test.go                             |  610 ++++
 nodes/testdata/n8n_chat_model_options.json         |   38 +
 nodes/testdata/n8n_sql_options.json                |   17 +
 nodes/transform.go                                 |  745 ++++
 nodes/transform_test.go                            |  315 ++
 nodes/unsupported.go                               |    4 +
 nodes/wait.go                                      |  228 ++
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
 pkg/sdk/.gitkeep                                   |    0
 pkg/sdk/doc.go                                     |   49 +
 pkg/sdk/example/echo/main.go                       |   35 +
 pkg/sdk/sdk.go                                     |   75 +
 pkg/sdk/sdk_test.go                                |   80 +
 pkg/sdk/wasm_exec_test.go                          |   86 +
 scripts/config-reference.go                        |  402 ++
 scripts/config-reference_test.go                   |   87 +
 scripts/docker-tags.sh                             |   84 +
 scripts/e2e-stub.mjs                               |   50 +
 scripts/generate-api-reference.mjs                 |  394 ++
 scripts/smoke-docker.sh                            |   13 +-
 scripts/smoke-postgres.sh                          |   43 +-
 sdk/CHANGELOG.md                                   |   23 +
 sdk/README.md                                      |  150 +-
 sdk/examples/host-page/README.md                   |   26 +-
 sdk/examples/host-page/index.html                  |   31 +-
 sdk/examples/host-page/package.json                |   13 +
 sdk/examples/host-page/server.mjs                  |   31 +-
 sdk/examples/reference-host/README.md              |  100 +
 sdk/examples/reference-host/package.json           |   13 +
 sdk/examples/reference-host/server.mjs             |  387 ++
 sdk/examples/reference-host/tenant.html            |  101 +
 sdk/package.json                                   |   22 +-
 sdk/scripts/dump-openapi.mjs                       |   13 +
 sdk/src/browser.ts                                 |  120 +-
 sdk/src/generated/models.ts                        | 2823 +++++++++++++-
 sdk/src/http.ts                                    |   31 +-
 sdk/src/server.ts                                  |  304 +-
 sdk/src/version.ts                                 |   15 +-
 sdk/test/browser.test.ts                           |  121 +
 sdk/test/operation-coverage.test.mjs               |  138 +
 sdk/test/operations.test.ts                        |  220 ++
 sdk/test/server.test.ts                            |   90 +-
 sdk/test/version.test.mjs                          |   40 +
 sidecar/doc.go                                     |   49 +
 sidecar/fixture/echo.js                            |   74 +
 sidecar/fixture_test.go                            |   98 +
 sidecar/protocol.go                                |   98 +
 sidecar/sidecar.go                                 |  494 +++
 sidecar/sidecar_test.go                            |  433 +++
 web/src/lib/api/generated/auth/auth.ts             |  752 ++++
 .../lib/api/generated/credentials/credentials.ts   |  199 +-
 .../datastore-columns/datastore-columns.ts         |  363 ++
 .../api/generated/datastore-rows/datastore-rows.ts |  691 ++++
 web/src/lib/api/generated/datastores/datastores.ts |  651 ++++
 web/src/lib/api/generated/models/aPIKeyResource.ts |   20 +
 .../lib/api/generated/models/activationNotice.ts   |   13 +
 .../lib/api/generated/models/activationResource.ts |   23 +
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 .../generated/models/clearedDatastoreOutputBody.ts |   13 +
 web/src/lib/api/generated/models/condition.ts      |   14 +
 .../api/generated/models/createAPIKeyInputBody.ts  |   17 +
 .../generated/models/createDatastoreInputBody.ts   |   23 +
 .../models/createStreamTicketInputBody.ts          |   17 +
 .../api/generated/models/createdAPIKeyResource.ts  |   18 +
 .../api/generated/models/credentialRequirement.ts  |   15 +
 .../api/generated/models/datastoreColumnInput.ts   |   22 +
 .../generated/models/datastoreColumnResource.ts    |   14 +
 .../generated/models/datastoreListOutputBody.ts    |   15 +
 .../lib/api/generated/models/datastoreResource.ts  |   17 +
 web/src/lib/api/generated/models/definition.ts     |   17 +
 .../api/generated/models/deleteRowsInputBody.ts    |   15 +
 .../api/generated/models/deleteRowsOutputBody.ts   |   16 +
 .../models/deleteRowsOutputBodyRowsItem.ts         |    9 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/executionResource.ts  |    4 +
 .../lib/api/generated/models/executionSummary.ts   |    2 +
 .../lib/api/generated/models/expressionGrammar.ts  |   16 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 web/src/lib/api/generated/models/filter.ts         |   14 +
 .../lib/api/generated/models/filterCondition.ts    |   13 +
 .../lib/api/generated/models/getDatastoreRow200.ts |    9 +
 .../lib/api/generated/models/getNodeIconParams.ts  |   19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |   15 +
 web/src/lib/api/generated/models/index.ts          |   70 +
 .../api/generated/models/insertDatastoreRow201.ts  |    9 +
 .../lib/api/generated/models/insertRowInputBody.ts |   15 +
 .../generated/models/insertRowInputBodyValues.ts   |   12 +
 .../api/generated/models/listAPIKeysOutputBody.ts  |   15 +
 .../generated/models/listDatastoreRowsParams.ts    |   37 +
 .../generated/models/listWorkflowVersionsParams.ts |   20 +
 .../api/generated/models/loadOptionsInputBody.ts   |   22 +
 .../models/loadOptionsInputBodyParameters.ts       |    9 +
 .../api/generated/models/loadOptionsResource.ts    |   17 +
 .../lib/api/generated/models/loadSchemaResource.ts |   17 +
 web/src/lib/api/generated/models/loginInputBody.ts |   24 +
 web/src/lib/api/generated/models/mapperColumn.ts   |   21 +
 web/src/lib/api/generated/models/nodeCodex.ts      |   16 +
 .../api/generated/models/nodeCodexSubcategories.ts |    9 +
 web/src/lib/api/generated/models/nodeIcon.ts       |   12 +
 web/src/lib/api/generated/models/option.ts         |   12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |   21 +
 web/src/lib/api/generated/models/port.ts           |    9 +-
 .../lib/api/generated/models/principalResource.ts  |   24 +
 .../lib/api/generated/models/propertyDefinition.ts |   19 +
 web/src/lib/api/generated/models/propertyGroup.ts  |   15 +
 web/src/lib/api/generated/models/propertyMode.ts   |   19 +
 .../generated/models/publishVersionInputBody.ts    |   17 +
 .../models/renameDatastoreColumnInputBody.ts       |   17 +
 .../generated/models/renameDatastoreInputBody.ts   |   17 +
 .../generated/models/resourceMapperDeclaration.ts  |   15 +
 .../lib/api/generated/models/rowListOutputBody.ts  |   16 +
 .../generated/models/rowListOutputBodyItemsItem.ts |    9 +
 .../models/streamExecutionEvents200Item.ts         |    9 +
 .../api/generated/models/streamTicketResource.ts   |   16 +
 .../api/generated/models/testCredentialResource.ts |   17 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
 .../api/generated/models/updateRowsInputBody.ts    |   18 +
 .../generated/models/updateRowsInputBodyValues.ts  |   12 +
 .../api/generated/models/updateRowsOutputBody.ts   |   16 +
 .../models/updateRowsOutputBodyRowsItem.ts         |    9 +
 .../lib/api/generated/models/upsertRowInputBody.ts |   18 +
 .../generated/models/upsertRowInputBodyValues.ts   |   12 +
 .../api/generated/models/upsertRowOutputBody.ts    |   17 +
 .../models/upsertRowOutputBodyRowsItem.ts          |    9 +
 web/src/lib/api/generated/models/visibility.ts     |   15 +
 .../lib/api/generated/models/webhookDeclaration.ts |   15 +
 .../models/workflowPublishEventResource.ts         |   19 +
 .../models/workflowPublishEventResourceAction.ts   |   16 +
 .../models/workflowVersionListResource.ts          |   17 +
 .../models/workflowVersionSummaryResource.ts       |   23 +
 web/src/lib/api/generated/nodes/nodes.ts           |  443 ++-
 .../workflow-lifecycle/workflow-lifecycle.ts       |  317 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  113 +-
 web/src/lib/api/http.test.ts                       |   79 +-
 web/src/lib/api/http.ts                            |   23 +
 .../lib/components/dashboard/dashboard-nav.svelte  |   12 +-
 .../lib/components/dashboard/list-states.svelte    |  136 +
 web/src/lib/components/ui/table/index.ts           |   28 +
 web/src/lib/components/ui/table/table-body.svelte  |   15 +
 .../lib/components/ui/table/table-caption.svelte   |   20 +
 web/src/lib/components/ui/table/table-cell.svelte  |   15 +
 .../lib/components/ui/table/table-footer.svelte    |   20 +
 web/src/lib/components/ui/table/table-head.svelte  |   15 +
 .../lib/components/ui/table/table-header.svelte    |   20 +
 web/src/lib/components/ui/table/table-row.svelte   |   15 +
 web/src/lib/components/ui/table/table.svelte       |   17 +
 .../workflow-editor/activation-notices.svelte      |  120 +
 .../components/workflow-editor/canvas-node.svelte  |   45 +-
 .../workflow-editor/execution-canvas-node.svelte   |   20 +-
 .../components/workflow-editor/node-icon.svelte    |   10 +-
 .../components/workflow-editor/node-picker.svelte  |    8 +-
 .../workflow-editor/properties-panel.svelte        |   61 +-
 .../workflow-editor/property-field.svelte          |  586 ++-
 .../workflow-editor/version-panel.svelte           |  509 +++
 .../workflow-editor/workflow-editor.svelte         |  290 +-
 web/src/lib/dashboard/cursor-page.test.ts          |   71 +
 web/src/lib/dashboard/cursor-page.ts               |   64 +
 web/src/lib/dashboard/list-state.test.ts           |   65 +
 web/src/lib/dashboard/list-state.ts                |   69 +
 web/src/lib/dashboard/request-guard.test.ts        |   55 +
 web/src/lib/dashboard/request-guard.ts             |   44 +
 web/src/lib/embed/embed-editor.svelte              |   50 +-
 web/src/lib/embed/session.svelte.ts                |   22 +-
 web/src/lib/embed/session.test.ts                  |   35 +-
 web/src/lib/workflow-editor/activation.test.ts     |  130 +
 web/src/lib/workflow-editor/activation.ts          |  108 +
 web/src/lib/workflow-editor/assignments.test.ts    |   82 +
 web/src/lib/workflow-editor/assignments.ts         |   89 +
 web/src/lib/workflow-editor/collection.test.ts     |  131 +
 web/src/lib/workflow-editor/collection.ts          |  148 +
 web/src/lib/workflow-editor/conditions.test.ts     |   82 +
 web/src/lib/workflow-editor/conditions.ts          |   80 +
 web/src/lib/workflow-editor/credentials.ts         |   41 +-
 web/src/lib/workflow-editor/document.test.ts       |   10 +-
 web/src/lib/workflow-editor/document.ts            |   14 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |    1 +
 web/src/lib/workflow-editor/event-stream.test.ts   |    2 +-
 web/src/lib/workflow-editor/execution.test.ts      |   72 +-
 web/src/lib/workflow-editor/execution.ts           |   81 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   43 +
 .../lib/workflow-editor/fixed-collection.test.ts   |  131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |   75 +
 web/src/lib/workflow-editor/history-diff.test.ts   |  273 ++
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 0 -> 10556 bytes
 web/src/lib/workflow-editor/node-visual.test.ts    |  186 +-
 web/src/lib/workflow-editor/node-visual.ts         |  228 +-
 web/src/lib/workflow-editor/ports.test.ts          |   98 +-
 web/src/lib/workflow-editor/ports.ts               |   34 +-
 .../lib/workflow-editor/resource-locator.test.ts   |  112 +
 web/src/lib/workflow-editor/resource-locator.ts    |  100 +
 .../lib/workflow-editor/resource-mapper.test.ts    |  121 +
 web/src/lib/workflow-editor/resource-mapper.ts     |  111 +
 .../lib/workflow-editor/version-history.test.ts    |  191 +
 web/src/lib/workflow-editor/version-history.ts     |  156 +
 web/src/lib/workflow-editor/visibility.test.ts     |   35 +
 web/src/lib/workflow-editor/visibility.ts          |  199 +
 web/src/routes/(dashboard)/+layout.svelte          |    1 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |  141 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  130 +-
 .../app/workflows/[id]/export-dialog.svelte        |  118 +
 .../app/workflows/diagnostics-section.svelte       |  120 +
 .../(dashboard)/app/workflows/import-dialog.svelte |  204 ++
 .../(dashboard)/app/workflows/import-report.svelte |  126 +
 .../routes/(dashboard)/credentials/+page.svelte    |   54 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  192 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   43 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   54 +-
 web/src/routes/+page.svelte                        |    8 +-
 902 files changed, 175210 insertions(+), 3012 deletions(-)
```
