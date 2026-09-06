---
id: FEAT-ss44d9
title: Build the Datastore storage engine with dialect-aware DDL
status: done
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-gvn62x
    - FEAT-r6xhnp
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-06T04:16:29Z"
---

## Scope

There is no `internal/datastore` package. A search for `datastore` across every Go file and the whole of `web/src` returns zero hits, and nothing in the product emits DDL at run time: `internal/database/database.go`'s `Migrate` is one call to `db.AutoMigrate(models...)` over the seven models `repository.Models()` returns. The owner has chosen the n8n storage model — one physical table per datastore, created by runtime DDL under the `kflow_` prefix, with a two-table catalogue — so this ticket builds the layer that turns a definition into a table.

It owns two catalogue models — `datastores`, and `datastore_columns` with an explicit integer `index` preserving column order — and a DDL service: create table with columns, drop table, add, rename and drop column, table exists. Physical types match n8n so a datastore stays portable: PostgreSQL `TEXT`, `DOUBLE PRECISION`, `BOOLEAN` and `TIMESTAMPTZ(3)`; SQLite `TEXT`, `REAL`, `BOOLEAN` as 0 or 1, and `DATETIME(3)`, with SQLite's date serialisation and boolean normalisation applied on read.

Identifier handling is the correctness boundary and the security boundary at once. `internal/workflow/ids.go` returns `prefix + "_" + value.String()` — a prefix plus a 36-character UUIDv7 — so a public datastore id is 39 bytes, and `kflow_` (6) plus `datastore_user_` (15) plus 39 is 60 of PostgreSQL's 63-byte limit, leaving three for an index suffix. The rule that follows is load-bearing: the physical table name comes from a short opaque surrogate and never from the public id. `kflow_ds_` plus sixteen hex characters is 25 bytes and leaves the budget wide open.

The remaining rules sit around that one. Column names match `^[a-zA-Z][a-zA-Z0-9_]*$` within 63 bytes, quoting doubles any embedded quote, no index is left for PostgreSQL to name, and `id`, `createdAt`, `updatedAt` and `dryRunState` are reserved case-insensitively.

This matters because the reason physical tables were chosen is that a host application can read a datastore with ordinary SQL. That promise is only worth making if the names are stable, bounded and predictable — an install where two datastores silently share an index is one where the host's own queries read a table nobody can describe.

## Acceptance criteria

- [ ] Creating a datastore produces one physical table named from a short opaque surrogate, and a test asserts the public datastore id appears in no table, index or constraint identifier.
- [ ] One test enumerates every identifier the DDL path can emit at the maximum configured table prefix and asserts each is within 63 bytes and the set stays pairwise unique after truncation to 63.
- [ ] Every index the service creates carries a name the service chose, proven by a test asserting that no emitted `CREATE INDEX` statement omits an index name.
- [ ] A column named `id`, `CreatedAt`, `UPDATEDAT` or `dryrunstate` is refused with a message naming the reserved word, proven by a case-permuted table-driven test.
- [ ] A column name outside `^[a-zA-Z][a-zA-Z0-9_]*$`, longer than 63 bytes, or differing from an existing column only in case is refused before any SQL is composed, proven by a test whose cases include an embedded double quote.
- [ ] The same definition yields the PostgreSQL and SQLite type sets named in Scope and a boolean round-trips as a Go `bool` on both, proven by a driver-parameterised test that reaches PostgreSQL when `KILASFLOW_TEST_POSTGRES_DSN` is set.
- [ ] A DDL failure leaves no catalogue row and a catalogue failure leaves no physical table, proven by a test injecting a failure at each point inside the single transaction.
- [ ] Dropping a datastore removes the catalogue rows and the physical table together, and a second drop of the same datastore is a no-op rather than an error, proven by a test.

## Implementation Plan

Build the identifier layer first — surrogate minting, quoting, the reserved-word check, deterministic index naming and the byte budget — in one file that never imports GORM. Everything else here composes SQL strings, and a naming rule added afterwards has to be retrofitted into statements already written and already passing their tests.

**Surrogate shape.** Two candidates: a per-tenant sequence, or sixteen hex characters. Recommend sixteen hex characters of a fresh random value, held in the catalogue under a unique constraint with a retry on conflict. Reject the per-tenant sequence: it needs a monotonic counter with its own transactional write path, which on SQLite contends for the single connection the whole process shares. Never derive the surrogate by hashing the datastore id — a hash is deterministic, so a collision is permanent rather than retryable. Name indexes from that surrogate and the column's catalogue `index` integer, never from the column name, because a 63-byte column name appended to a 25-byte table name is 90 bytes before any suffix.

Issue the DDL with `tx.Exec` over the identifier layer's output. Reject GORM's `db.Migrator()`: it reflects over a Go struct to decide columns and types, and a datastore has no struct. Both internal drivers execute DDL transactionally, so the ordering — catalogue write first, DDL second, one transaction — holds on both; say so in the package doc, because a reader arriving from `internal/sqlnode` has been working against MySQL, which does not.

The trap is that PostgreSQL truncates an over-length identifier to 63 bytes without erroring. Two index names differing only past byte 63 collapse into one, `CREATE INDEX IF NOT EXISTS` finds the truncated name taken and skips the second without complaint, and the service records an index that does not exist. Nothing fails; the query that needed it does a sequential scan for the life of the install, on a table the operator believes is indexed. Test the property, not examples: no hand-picked set contains the pair that collides.

Handle case deliberately. SQLite folds ASCII identifiers case-insensitively while PostgreSQL treats a quoted identifier as case-sensitive, so `"createdAt"` and `"CreatedAt"` are two columns on one driver and one on the other; reserving the four system names case-insensitively and rejecting user columns differing only in case is what makes a definition mean the same thing on both. One reference gap to close before writing the type table: `packages/cli/src/modules/` in the widened n8n checkout holds only `community-packages`, so the `data-table` module the roadmap cites is not on disk.

**Where the prefix comes from.** Recommend consuming `database.table_prefix` from V2-p6-2 through a single accessor rather than threading it into every DDL call — `config.Database` today carries only `Driver`, `DSN`, `MaxOpenConns` and `MaxIdleConns`, so the key does not exist yet and this ticket must not invent a second one. What would reopen it: V2-p6-2 landing with the prefix reachable only through GORM's namer, since the DDL path is not GORM and would then need its own accessor.

## References

- Roadmap plan, p9 section, entry V2-p9-1: `.pine/roadmap.md`.
- `internal/workflow/ids.go` — `NewID`, a prefix plus a 36-character UUIDv7, the identifier that must stay out of every physical name.
- `internal/database/database.go` — lines 57-63 pin SQLite to one connection for the whole process, and `Migrate` is the repository's only `AutoMigrate`.
- `internal/repository/models.go` — `Models()` and the seven `TableName() string` methods, the naming convention the two catalogue models follow.
- `internal/repository/workflows.go` — `TenantScope` at lines 21-23, and the `store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error` pattern the catalogue-plus-DDL transaction reuses.
- `internal/database/database_test.go` — lines 145-150, the `KILASFLOW_TEST_POSTGRES_DSN` gate and the cleanup that drops only `models[0..3]`, leaking `credentials`, `webhook_bindings` and `schedules`; correct it here, since V2-p9-0 leaves it to the first p9 ticket that opens the database layer.
- `.pine/tickets/FEAT-r6xhnp.md` (V2-p6-2) — the `table_prefix` cap and the identifier-budget criterion this DDL path must satisfy.
- `.pine/tickets/FEAT-gvn62x.md` (V2-p6-1) — the goose runner the catalogue tables join, and the migration authority the runtime-DDL carve-out is measured against.
- `go.mod` — `github.com/glebarez/sqlite v1.11.0` over `modernc.org/sqlite v1.23.1`, the pure-Go driver whose boolean and datetime handling the read path normalises.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 20-23 — a fresh table already carrying `id`, `createdAt` and `updatedAt`; the Add Column dialog offering no nullability, uniqueness or default; the four column types verbatim (`string`, `number`, `boolean`, `datetime`, where the create dialog says `datetime` and the filter list says `date` — `date` is the wire value, so do not copy the UI label into the contract); and a new row arriving as `id=1` with both timestamps set by the database. Captured from a live local n8n 2.x instance; gitignored, never vendored.

## Progress (DatastoreEngine, 2026-09-06)

Status: done. Engine + DDL only; no catalogue API, row queries, node, or import.
`internal/database/migrate.go` untouched (new migration files are picked up
automatically, so append-only holds trivially).

Added:
- `migrations/sqlite+postgres/000005_datastores.{up,down}.sql` — `datastores`
  (id, tenant_id, name, surrogate UNIQUE, schema_version DEFAULT 1, stamps)
  and `datastore_columns` (datastore_id FK CASCADE, name, type wire value,
  position) plus surrogate-unique, tenant, and per-datastore indexes. Spaces
  indent; plain CREATE TABLE per 000003 rationale. `schema_version` is the
  column FEAT-gxppx1's plan asks V2-p9-1 to introduce.
- `internal/datastore/doc.go` — package doc incl. catalogue-first/DDL-second
  single-transaction ordering on both drivers.
- `internal/datastore/idents.go` (no GORM) — wire types
  string/number/boolean/date with `datetime` accepted as alias and normalised
  to the `date` wire value; name pattern `^[a-zA-Z][a-zA-Z0-9_]*$`, 63-byte
  cap, case-insensitive reservation of id/createdAt/updatedAt/dryRunState
  with the canonical word in the error; 16-hex random surrogate (retry on
  unique conflict, never hashed); `PhysicalTableName = prefix+ds_+surrogate`,
  explicit PK name, surrogate+position index rule; dialect quoting; PG
  TEXT/DOUBLE PRECISION/BOOLEAN/TIMESTAMPTZ(3) vs SQLite
  TEXT/REAL/BOOLEAN/DATETIME(3).
- `internal/datastore/model.go` — catalogue models via TablerWithNamer
  (prefix rides the handle); deliberately outside `repository.Models()` so
  the drift test is unaffected.
- `internal/datastore/engine.go` — Create/Drop/AddColumn/RenameColumn/
  DropColumn/TableExists; prefix threaded once at NewEngine (validated
  against `config.MaxTablePrefixLength`); auto-increment PK (sqlite
  AUTOINCREMENT rowid alias, PG bigserial + explicitly named PK constraint);
  DB-set createdAt/updatedAt defaults; nullable user columns, no defaults.
- `internal/datastore/fleet.go` — FEAT-gxppx1 hook: `CurrentSchemaVersion`,
  per-datastore step registry, transactional step+stamp, unknown-version
  refusal naming both versions. No-op path only.

Tests (15, all passing):
- `go test ./internal/datastore/... ./internal/database/...` green on
  sqlite; `go vet` clean on the new package.
- Live PG16 (docker, per live-databases.md): full suite green incl.
  `TestCreateInsertReadDropRoundTrips/{sqlite,postgres}` — boolean
  round-trips as Go bool on both, id starts at 1, timestamps DB-set.
- Fix applied with Main approval (mechanical, test-only):
  `internal/database/migrate_test.go` `postBaselineTables` +=
  `datastore_columns`, `datastores` (newest/child-first, per that file's own
  comment). Without it every PG-gated database test after the first fails
  with `relation "datastores" already exists` — proven on pristine PG with
  my tests never run.
- Shared-server finding: `go test` runs package binaries in parallel, and
  both suites reset the same PG server, so the combined PG run is racy by
  construction (1 flake in ~5 runs). Serialized (`go test -p 1 ...`) it is
  3/3 green. Whoever wires PG into CI should use `-p 1` for these packages.
- Container `kf-pg` left running on 55433 with the documented DSN for
  re-verification.

Needs (for later owners, not this ticket): row read/write + SQLite
boolean/date normalisation helpers (FEAT-nrfg6e); catalogue HTTP API;
fleet steps/leases/readiness (FEAT-gxppx1); call-site wiring passing
`cfg.TablePrefix` to `NewEngine`.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-06.

- Base: `4816f2ed` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `cf72f8d0` — chore(pine): open phase p9 for the Datastore and route database parity to p4
- Files changed (base → working tree):

```
 .env.example                                       |  186 +
 .github/actions/js-toolchain/action.yml            |   49 +
 .github/workflows/ci.yml                           |  429 +++
 .github/workflows/release.yml                      |   92 +
 .gitignore                                         |    3 +
 .pine/CHECKPOINT.md                                |  148 +
 .pine/MEMORY.md                                    |    7 +
 .pine/memory/code-node.md                          |   74 +
 .pine/memory/docker.md                             |    9 +
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
 .pine/tickets/FEAT-1axhdn.md                       |   65 +
 .pine/tickets/FEAT-1br8at.md                       |    2 +-
 .pine/tickets/FEAT-1c70nt.md                       |   73 +
 .pine/tickets/FEAT-27km39.md                       |  730 ++++
 .pine/tickets/FEAT-2f68r8.md                       |   83 +-
 .pine/tickets/FEAT-2phs15.md                       |  461 +++
 .pine/tickets/FEAT-347egc.md                       |  806 +++-
 .pine/tickets/FEAT-3taswf.md                       |   67 +
 .pine/tickets/FEAT-3xqky1.md                       |   70 +
 .pine/tickets/FEAT-45tfmh.md                       |  438 +++
 .pine/tickets/FEAT-48hreg.md                       |   10 +-
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
 .pine/tickets/FEAT-5z37xh.md                       |  121 +
 .pine/tickets/FEAT-68zzqs.md                       |  438 +++
 .pine/tickets/FEAT-6vfn3s.md                       |  356 +-
 .pine/tickets/FEAT-7cg0cd.md                       |    3 +-
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
 .pine/tickets/FEAT-a94c8y.md                       |    2 +-
 .pine/tickets/FEAT-adzn0a.md                       |   76 +-
 .pine/tickets/FEAT-afs850.md                       |    3 +-
 .pine/tickets/FEAT-agj52c.md                       |   64 +
 .pine/tickets/FEAT-ajw7wt.md                       |  122 +-
 .pine/tickets/FEAT-az620p.md                       |  450 ++-
 .pine/tickets/FEAT-bp0ytb.md                       |  340 +-
 .pine/tickets/FEAT-bscygc.md                       |  713 ++++
 .pine/tickets/FEAT-c2a081.md                       |    2 +-
 .pine/tickets/FEAT-cgm1y3.md                       |  786 +++-
 .pine/tickets/FEAT-cjpbe6.md                       |   70 +
 .pine/tickets/FEAT-cpdp8y.md                       |   70 +
 .pine/tickets/FEAT-csqgg5.md                       |    5 +-
 .pine/tickets/FEAT-cwz4ac.md                       |   66 +
 .pine/tickets/FEAT-cx3hq1.md                       |  712 ++++
 .pine/tickets/FEAT-czbzs6.md                       |   86 +
 .pine/tickets/FEAT-ddzk2k.md                       |   77 +-
 .pine/tickets/FEAT-de8d4c.md                       |   71 +
 .pine/tickets/FEAT-ed6wdy.md                       |   66 +
 .pine/tickets/FEAT-ej0468.md                       |  826 ++++-
 .pine/tickets/FEAT-frvez8.md                       |   70 +
 .pine/tickets/FEAT-fw0m2q.md                       |    2 +-
 .pine/tickets/FEAT-g6wrxm.md                       |  196 +
 .pine/tickets/FEAT-gg85se.md                       |   77 +
 .pine/tickets/FEAT-gjzgkd.md                       |    2 +-
 .pine/tickets/FEAT-gvn62x.md                       |  146 +-
 .pine/tickets/FEAT-gxppx1.md                       |   71 +
 .pine/tickets/FEAT-hv4q8e.md                       |    2 +-
 .pine/tickets/FEAT-je4f4t.md                       |  788 +++-
 .pine/tickets/FEAT-jq84xk.md                       |   67 +
 .pine/tickets/FEAT-jwhdsy.md                       |  414 ++-
 .pine/tickets/FEAT-k3fmj1.md                       |    3 +-
 .pine/tickets/FEAT-k3grr5.md                       |    2 +-
 .pine/tickets/FEAT-k65hqv.md                       |    2 +-
 .pine/tickets/FEAT-k9dwgn.md                       |   65 +
 .pine/tickets/FEAT-knpfqf.md                       |    2 +-
 .pine/tickets/FEAT-kwxxd0.md                       |  141 +
 .pine/tickets/FEAT-m94hhx.md                       |  265 ++
 .pine/tickets/FEAT-mvegj5.md                       |   76 +-
 .pine/tickets/FEAT-n19dch.md                       |   66 +
 .pine/tickets/FEAT-n5fdz3.md                       |  458 +++
 .pine/tickets/FEAT-nbqye0.md                       |    2 +-
 .pine/tickets/FEAT-nc6z9r.md                       |   68 +
 .pine/tickets/FEAT-nch9dg.md                       |   67 +
 .pine/tickets/FEAT-nrfg6e.md                       |   69 +
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
 .pine/tickets/FEAT-rj17xj.md                       |    2 +-
 .pine/tickets/FEAT-sar60r.md                       |   90 +-
 .pine/tickets/FEAT-sbnejr.md                       |  789 +++-
 .pine/tickets/FEAT-sdjdh2.md                       |   82 +
 .pine/tickets/FEAT-sfy1tq.md                       |  139 +
 .pine/tickets/FEAT-snxxny.md                       |  409 +++
 .pine/tickets/FEAT-sp8cfm.md                       |  361 +-
 .pine/tickets/FEAT-ss44d9.md                       |  127 +
 .pine/tickets/FEAT-t26rt7.md                       |   65 +
 .pine/tickets/FEAT-t5q318.md                       |    2 +-
 .pine/tickets/FEAT-v8k1tc.md                       |   90 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  398 +-
 .pine/tickets/FEAT-w9kqeg.md                       |    2 +-
 .pine/tickets/FEAT-whn5vb.md                       |  103 +-
 .pine/tickets/FEAT-wkmv5e.md                       |   67 +
 .pine/tickets/FEAT-xeq6st.md                       |   68 +
 .pine/tickets/FEAT-xqqjqv.md                       |  327 ++
 .pine/tickets/FEAT-xr7ga9.md                       |   79 +
 .pine/tickets/FEAT-xx6p22.md                       |  117 +
 .pine/tickets/FEAT-ybm2pd.md                       |   68 +-
 .pine/tickets/FEAT-ykyfbd.md                       |   72 +
 .pine/tickets/FEAT-yx0qt6.md                       |  110 +
 .pine/tickets/FEAT-yyjfjq.md                       |    3 +-
 .pine/tickets/FEAT-za118x.md                       |   80 +
 .pine/tickets/FEAT-zmfsjd.md                       |  146 +
 .pine/tickets/FEAT-znm60y.md                       |  316 +-
 .pine/tickets/FEAT-ztxs5p.md                       |  347 +-
 Dockerfile                                         |   54 +-
 Makefile                                           |  243 +-
 README.md                                          |  276 +-
 cmd/kilasflow/main.go                              |  470 ++-
 cmd/kilasflow/main_test.go                         |   54 +-
 cmd/nodepackgen/generate.go                        |  576 +++
 cmd/nodepackgen/generate_test.go                   |  374 ++
 cmd/nodepackgen/main.go                            |  165 +
 cmd/nodepackgen/openapi.go                         |  168 +
 cmd/nodepackgen/testdata/manifest.json             |   12 +
 cmd/nodepackgen/testdata/pack.golden.json          |  238 ++
 cmd/nodepackgen/testdata/report.golden.md          |   20 +
 cmd/nodepackgen/testdata/spec.json                 |   98 +
 compose.build.yaml                                 |   35 +
 compose.postgres.yaml                              |   73 +
 compose.yaml                                       |  103 +
 config.example.yaml                                |  293 +-
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
 docs/src/content/docs/guides/embedding.md          |   49 +
 docs/src/content/docs/guides/n8n-migration.md      |  674 ++++
 docs/src/content/docs/guides/node-authoring.md     |   41 +
 docs/src/content/docs/index.mdx                    |   59 +
 .../docs/operate/configuration-reference.md        |  667 ++++
 docs/src/content/docs/operate/configuration.md     |   71 +
 docs/src/content/docs/operate/deployment.md        |  101 +
 docs/src/content/docs/operate/security.md          |   88 +
 docs/src/content/docs/operate/upgrades.md          |   69 +
 docs/src/content/docs/reference/api-contract.md    |  306 ++
 docs/src/content/docs/reference/api.md             |   41 +
 .../content/docs/reference/expression-grammar.md   |  183 +
 docs/src/content/docs/reference/node-packs.md      |   36 +
 docs/src/content/docs/start/first-workflow.md      |   40 +
 docs/src/content/docs/start/install.md             |  271 ++
 docs/src/content/docs/start/what-kilasflow-is.md   |   84 +
 docs/src/styles/kilasflow.css                      |  138 +
 docs/tsconfig.json                                 |    5 +
 e2e/.gitignore                                     |    2 +
 e2e/fixtures.ts                                    |   40 +
 e2e/global-setup.ts                                |   27 +
 e2e/helpers/seed.ts                                |  174 +
 e2e/helpers/server.ts                              |  140 +
 e2e/helpers/stub.ts                                |   98 +
 e2e/package.json                                   |   14 +
 e2e/playwright.config.ts                           |   32 +
 e2e/pnpm-lock.yaml                                 |   57 +
 e2e/tests/smoke.spec.ts                            |  108 +
 executions-narrow.png                              |  Bin 0 -> 37917 bytes
 gflow-prd-v1.md                                    |   32 +
 go.mod                                             |    9 +-
 go.sum                                             |   37 +-
 internal/ai/agent.go                               |  146 +-
 internal/ai/ai.go                                  |   50 +-
 internal/ai/ai_test.go                             |  166 +
 internal/ai/maf/doc.go                             |   15 +-
 internal/ai/memory.go                              |  164 +-
 internal/ai/openai.go                              |  100 +-
 internal/ai/openai_test.go                         |  156 +
 internal/api/auth_test.go                          |  668 ++++
 internal/api/credentials_test.go                   |  401 ++
 internal/api/embed_test.go                         |   61 +-
 internal/api/handlers/auth.go                      |  380 ++
 internal/api/handlers/credentials.go               |  339 ++
 internal/api/handlers/executions.go                |   40 +-
 internal/api/handlers/interop.go                   |    5 +-
 internal/api/handlers/nodes.go                     |  468 ++-
 internal/api/handlers/tenants.go                   |   46 +
 internal/api/handlers/workflows.go                 |  299 +-
 internal/api/handlers/workflows_delete_test.go     |  111 +
 internal/api/middleware/auth.go                    |  173 +
 internal/api/middleware/embed.go                   |   10 +-
 internal/api/node_types_test.go                    |  424 +++
 internal/api/routes.go                             |   58 +-
 internal/api/server.go                             |   59 +-
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
 internal/config/config.go                          |  530 ++-
 internal/config/config_test.go                     |  325 ++
 internal/credentials/builtin.go                    |  189 +
 internal/credentials/credentials.go                |   98 +-
 internal/credentials/credentials_test.go           |  244 ++
 internal/credentials/registry.go                   |  346 ++
 internal/database/database.go                      |   71 +-
 internal/database/database_test.go                 |   91 +-
 internal/database/migrate.go                       |  648 ++++
 internal/database/migrate_test.go                  |  881 +++++
 internal/database/prefix_test.go                   |  371 ++
 internal/datetime/datetime_test.go                 |  134 +
 internal/datetime/doc.go                           |   15 +
 internal/datetime/format.go                        |  195 +
 internal/datetime/parse.go                         |  108 +
 internal/embed/embed.go                            |    2 +
 internal/engine/authenticate.go                    |  104 +
 internal/engine/runner.go                          |  467 ++-
 internal/engine/runner_test.go                     |  579 ++-
 internal/engine/service.go                         |  344 +-
 internal/engine/service_test.go                    |   41 +-
 internal/engine/subworkflow_test.go                |  329 ++
 internal/engine/worker_test.go                     |    5 +
 internal/execution/records.go                      |   41 +-
 internal/expression/doc.go                         |   91 +-
 internal/expression/expression.go                  |  350 +-
 internal/expression/expression_test.go             |  374 +-
 internal/expression/functions.go                   |  219 ++
 internal/expression/roots.go                       |  125 +
 internal/expression/undefined.go                   |   22 +
 internal/guardrails/shell_injection_test.go        |   70 +
 internal/interop/n8n/corpus/BASELINE.md            |   43 +-
 internal/interop/n8n/corpus/baseline.json          |  113 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |  107 +-
 internal/interop/n8n/export_test.go                |   15 +
 internal/interop/n8n/n8n.go                        |  747 +++-
 internal/interop/n8n/n8n_test.go                   | 2359 +++++++++++-
 internal/interop/n8n/parameters.go                 | 2668 +++++++++++++-
 internal/interop/n8n/sqlfidelity_test.go           |  442 +++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |  110 +
 internal/loadoptions/loadoptions.go                |  412 +++
 internal/loadoptions/loadoptions_test.go           |  453 +++
 internal/loadoptions/schema.go                     |   68 +
 internal/loadoptions/sql.go                        |  287 ++
 internal/loadoptions/sql_test.go                   |  350 ++
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  716 +++-
 internal/node/registry_test.go                     |  816 ++++-
 internal/nodepack/nodepack.go                      |  432 +++
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
 internal/repository/auth.go                        |  330 ++
 internal/repository/auth_test.go                   |  322 ++
 internal/repository/execution_retention.go         |  180 +
 internal/repository/execution_retention_test.go    |  396 ++
 internal/repository/executions.go                  |  208 +-
 internal/repository/models.go                      |  253 +-
 internal/repository/models_test.go                 |   12 +-
 internal/repository/postgres_execution_test.go     |  213 ++
 internal/repository/prefix_test.go                 |   80 +
 internal/repository/schedules.go                   |  140 +-
 internal/repository/table_names_test.go            |   49 +
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
 internal/sqlnode/introspect.go                     |  240 ++
 internal/sqlnode/policy_test.go                    |  243 ++
 internal/sqlnode/sqlnode.go                        |  745 +++-
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
 migrations/sqlite/000001_baseline.down.sql         |   22 +
 migrations/sqlite/000001_baseline.up.sql           |  185 +
 migrations/sqlite/000002_workflow_history.down.sql |   11 +
 migrations/sqlite/000002_workflow_history.up.sql   |   29 +
 migrations/sqlite/000003_identity.down.sql         |   14 +
 migrations/sqlite/000003_identity.up.sql           |   73 +
 .../sqlite/000004_execution_indexes.down.sql       |    5 +
 migrations/sqlite/000004_execution_indexes.up.sql  |   21 +
 nodes/ai.go                                        | 2161 ++++++++++-
 nodes/ai_ollama_test.go                            |  413 +++
 nodes/ai_test.go                                   | 1435 +++++++-
 nodes/annotation.go                                |    3 +
 nodes/apostrophe_live_test.go                      |   43 +
 nodes/assignments.go                               |  180 +
 nodes/bindings_test.go                             |  129 +
 nodes/code.go                                      |  104 +-
 nodes/code_test.go                                 |  147 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  196 +-
 nodes/database.go                                  |  329 +-
 nodes/database_test.go                             |  751 +++-
 nodes/datetime.go                                  |  408 +++
 nodes/datetime_test.go                             |  274 ++
 nodes/executors.go                                 |  627 +++-
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
 nodes/wait.go                                      |  230 ++
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
 scripts/config-reference.go                        |  402 ++
 scripts/config-reference_test.go                   |   87 +
 scripts/docker-tags.sh                             |   84 +
 scripts/e2e-stub.mjs                               |   50 +
 scripts/smoke-docker.sh                            |   13 +-
 scripts/smoke-postgres.sh                          |   43 +-
 sdk/README.md                                      |   52 +-
 sdk/package.json                                   |    2 +-
 sdk/src/generated/models.ts                        | 1608 +++++++-
 sdk/src/server.ts                                  |  255 +-
 sdk/src/version.ts                                 |   15 +-
 web/src/lib/api/generated/auth/auth.ts             |  752 ++++
 .../lib/api/generated/credentials/credentials.ts   |  199 +-
 web/src/lib/api/generated/models/aPIKeyResource.ts |   20 +
 .../lib/api/generated/models/activationNotice.ts   |   13 +
 .../lib/api/generated/models/activationResource.ts |   23 +
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 web/src/lib/api/generated/models/condition.ts      |   14 +
 .../api/generated/models/createAPIKeyInputBody.ts  |   17 +
 .../models/createStreamTicketInputBody.ts          |   17 +
 .../api/generated/models/createdAPIKeyResource.ts  |   18 +
 .../api/generated/models/credentialRequirement.ts  |   15 +
 web/src/lib/api/generated/models/definition.ts     |   17 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/executionResource.ts  |    2 +
 .../lib/api/generated/models/executionSummary.ts   |    2 +
 .../lib/api/generated/models/expressionGrammar.ts  |   16 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |   19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |   15 +
 web/src/lib/api/generated/models/index.ts          |   41 +
 .../api/generated/models/listAPIKeysOutputBody.ts  |   15 +
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
 .../generated/models/resourceMapperDeclaration.ts  |   15 +
 .../api/generated/models/streamTicketResource.ts   |   16 +
 .../api/generated/models/testCredentialResource.ts |   17 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
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
 web/src/lib/workflow-editor/event-stream.test.ts   |    2 +-
 web/src/lib/workflow-editor/execution.test.ts      |   72 +-
 web/src/lib/workflow-editor/execution.ts           |   75 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   43 +
 .../lib/workflow-editor/fixed-collection.test.ts   |  131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |   75 +
 web/src/lib/workflow-editor/history-diff.test.ts   |  273 ++
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 0 -> 10556 bytes
 web/src/lib/workflow-editor/node-visual.test.ts    |  163 +-
 web/src/lib/workflow-editor/node-visual.ts         |  224 +-
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
 .../routes/(dashboard)/app/workflows/+page.svelte  |  141 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  130 +-
 .../app/workflows/[id]/export-dialog.svelte        |  118 +
 .../app/workflows/diagnostics-section.svelte       |  120 +
 .../(dashboard)/app/workflows/import-dialog.svelte |  204 ++
 .../(dashboard)/app/workflows/import-report.svelte |  126 +
 .../routes/(dashboard)/credentials/+page.svelte    |   54 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  190 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   33 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   54 +-
 web/src/routes/+page.svelte                        |    8 +-
 735 files changed, 120609 insertions(+), 2757 deletions(-)
```
