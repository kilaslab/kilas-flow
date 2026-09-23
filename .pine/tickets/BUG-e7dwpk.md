---
id: BUG-e7dwpk
title: Datastore names can collide (incl. case-only); 'By Name' silently acts on the wrong table
status: done
priority: high
labels:
    - datastore
    - data-integrity
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T04:33:16Z"
---

# Description

A Data table node set to By Name `leads` read the 46 rows of `Leads`. With Update, Delete or Clear, the write would hit the wrong table.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-3). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Data table names are unique within a project, so a name identifies exactly one table.

# Steps to Reproduce

1. Create datastore "[ux-ops] Leads" and import 45 rows. 2. Create "[ux-ops] leads", which is accepted, and a second "[ux-ops] <img src=x onerror=alert(1)> 📊", which is also accepted. 3. Build Manual → Data table (Row: Get) with Data table = By Name "[ux-ops] leads" and run it (wf_01a0cbdf-16e0-…).

# Expected

Refuse duplicate names (case-insensitive, since lookup is case-insensitive) on create and rename, or make By-Name fail when the name is ambiguous.

# Actual

No uniqueness check exists, not even for exact duplicates. The run succeeded and returned the 46 rows of "[ux-ops] Leads", not the empty "[ux-ops] leads". With Update, Delete or Clear, that write would hit the wrong table. The "From list" picker also shows identical labels for exact duplicates.

# Acceptance Criteria
- [x] A unique index on (tenant_id, lower(name)), with a friendly 409 in the create and rename dialogs
- [x] By-Name lookup errors when more than one table matches, and never picks the first
- [x] The "From list" picker disambiguates any existing duplicates

# Implementation Plan

Add a unique index on (tenant_id, lower(name)) with a friendly 409 in the create and rename dialogs. In datastoreID, error when more than one table matches.

# Notes

Related (from the audit): none

# Related Files

agents/ux-ops/21-datastore-dup.png; execution exec_01a0cbdf-16fb-7d7d-ab54-8f4130fe629e; nodes/datastore.go:783-802 (the first `strings.EqualFold` match wins).

## Progress — Lane E (2026-09-23)

Fix: one rule for when two data table names are the same (Go `strings.EqualFold` over trimmed names), enforced on write and applied on every By-Name read.

- **Catalogue** (`internal/datastore/names.go`, `engine.go`, `catalogue.go`): `Create` and `RenameDatastore` trim the name and refuse one the tenant already holds, case-insensitively, inside their transaction, with a `*NameTakenError` that unwraps to `ErrNameTaken` and names the table in the way. On `gorm.ErrDuplicatedKey`, both re-ask the unique index's own question (`lower(name)`), so a writer that lost the race is told the name is taken. That covers PostgreSQL's locale folding (`İ` → `i`), which `EqualFold` does not do. Before, create reported "surrogate kept colliding" after three retries and rename answered 500. A surrogate or id collision is still retried.
- **Migration 000021 `datastore_unique_names`** (SQLite and PostgreSQL): renames every case-insensitive duplicate except the oldest per tenant (`created_at`, then `id`) to `name || ' (' || id || ')'`, cut with `substr` to fit `varchar(255)` on PostgreSQL, then builds `uidx_datastores_tenant_lower_name` on `("tenant_id", lower("name"))`. The down migration drops the index and keeps the renames.
- **One resolver**: `datastore.ResolveByName(candidates, name)` returns the one match, the unknown-datastore refusal, or `ErrAmbiguousName` listing every id, and never the first match. It replaces the four copies: `nodes/datastore.go` `datastoreID` and `datastoreToolID`, the mapper fallback in `cmd/kilasflow/main.go`, and the confinement check. That check moved from `embed.Confinement.AllowsDatastoreName` to `nodes/embedscope.go` `grantsDatastoreName`, because `internal/datastore` already imports `internal/embed` through `internal/config`. A name two grants share still counts as granted: each grant alone allows it, and the run refuses the ambiguity against the live list.
- **API**: `problem()` answers a taken name with 409 "A data table named “X” already exists", where X is the holder's name. The create and rename dialogs already render `detail` through `message()`.
- **Picker**: a From-list label whose name the resolver would call ambiguous gets ` · <last 8 characters of the id>`.

Proof:
- `go test ./internal/datastore/ -run 'NameIsTaken|RenamingOnto|WrittenAroundTheEngine|NotTheNameIsRetried|LosesTheRace|OnlyTheDatabaseFolds|ResolveByName'` → ok (SQLite and PostgreSQL 17)
- `go test ./internal/database/ -run TestDuplicateDatastoreNamesAreRenamedBeforeTheyAreMadeUnique` → ok (both drivers)
- `go test ./internal/api/ -run TestADuplicateDatastoreNameIsAConflictNamingTheTableInTheWay` → ok
- `go test ./nodes/ -run 'ByNameRefusesANameTwoTablesShare|TestEmbedScopeIssuesComparesGrantedNamesTheWayARunResolvesThem'` → ok
- `go test ./internal/loadoptions/ -run TestDatastoreListLabelsTellTablesThatShareANameApart` → ok
- `go test ./...` → ok (SQLite). With `KILASFLOW_TEST_POSTGRES_DSN` set against a throwaway pgvector/pg17 container, these packages → ok: database, datastore, tenantpurge, api/..., loadoptions, nodes, embed, repository, cmd/kilasflow and cli.

## Progress — final review fixes (2026-09-23)

- **Migration 000021 groups duplicates the way the engine compares names.** It partitioned by `lower(name)`, but `sameName` also trims, so a legacy "Leads" beside "Leads " both kept their names and every By-Name reference to them was refused as ambiguous. Both dialects now partition by the lowered name trimmed of the ASCII whitespace `strings.TrimSpace` strips (space, `\t`, `\n`, `\v`, `\f`, `\r`): SQLite `trim(name, ' ' || char(9) || … || char(13))`, PostgreSQL `btrim(name, ' ' || chr(9) || … || chr(13))`. The oldest per group still keeps its name; the rename and PostgreSQL's `varchar(255)` cut are unchanged. The unique index stays on `lower(name)`: the grouping is coarser, so nothing it leaves can stop the index. `Create` and `RenameDatastore` already trim every name they write, so the catalogue needs no change.
- Proof: `TestDuplicateDatastoreNamesAreRenamedBeforeTheyAreMadeUniqueOn{SQLite,Postgres}` seeds "Tags", "Tags ", "\tTAGS" and " tags\r\n" and checks the three younger ones are renamed and that no two of a tenant's names are the same by the engine's rule. Red on both drivers before the change, green after.
- **A taken name answers 409, never 404.** `problem()` in `internal/api/handlers/datastores.go` asked `IsUnknown` (a match on the text "unknown datastore") before the `*NameTakenError` branch, and that error's text carries the name the tenant chose, so creating or renaming onto a taken name that said "unknown datastore" answered 404. The taken-name branch is now matched first. Proof: `go test ./internal/api/ -run TestATakenNameThatSaysUnknownDatastoreIsStillAConflict` (create and case-changed rename both 409 with the holder named), red before, green after.

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (2):
  - `46a52682` — BUG-e7dwpk: a data table's name belongs to one table in its tenant, and By Name refuses a name it cannot pin to one
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          | Bin 0 -> 119127 bytes
 .pine/memory/code-node.md                          |   3 +-
 .pine/memory/licensing.md                          |   2 +-
 .pine/memory/n8n-reference.md                      |   4 +-
 .pine/roadmap.md                                   |   8 +-
 .pine/tickets/BUG-0xv7bg.md                        |  54 +++
 .pine/tickets/BUG-15st2k.md                        | 162 +++++++
 .pine/tickets/BUG-2eryxn.md                        |  49 +++
 .pine/tickets/BUG-2mes2k.md                        |  54 +++
 .pine/tickets/BUG-2n4rfz.md                        |  51 +++
 .pine/tickets/BUG-2z8geh.md                        |  52 +++
 .pine/tickets/BUG-3k12ky.md                        | 163 +++++++
 .pine/tickets/BUG-3qxx0j.md                        |  59 +++
 .pine/tickets/BUG-56qqgx.md                        |  52 +++
 .pine/tickets/BUG-5bgx5c.md                        |  59 +++
 .pine/tickets/BUG-605n21.md                        | 131 ++++++
 .pine/tickets/BUG-66fhea.md                        |  97 +++++
 .pine/tickets/BUG-6d6wbg.md                        |  98 +++++
 .pine/tickets/BUG-6gkd12.md                        | 107 +++++
 .pine/tickets/BUG-719gaz.md                        |  88 ++++
 .pine/tickets/BUG-9dw5me.md                        |  53 +++
 .pine/tickets/BUG-9pmv8y.md                        |  61 +++
 .pine/tickets/BUG-b3p8va.md                        |  61 +++
 .pine/tickets/BUG-b4cb1c.md                        | 119 +++++
 .pine/tickets/BUG-b8bwhw.md                        |  55 +++
 .pine/tickets/BUG-bcahaj.md                        | 100 +++++
 .pine/tickets/BUG-bw2zc1.md                        |  55 +++
 .pine/tickets/BUG-dstsg9.md                        |  54 +++
 .pine/tickets/BUG-e7dwpk.md                        |  69 +++
 .pine/tickets/BUG-ecbq28.md                        | 111 +++++
 .pine/tickets/BUG-epy2se.md                        | 122 ++++++
 .pine/tickets/BUG-g7ffj1.md                        |  50 +++
 .pine/tickets/BUG-hmp85t.md                        |  99 +++++
 .pine/tickets/BUG-j7qrp2.md                        |  52 +++
 .pine/tickets/BUG-mzk0xn.md                        |  35 ++
 .pine/tickets/BUG-n6p7qy.md                        | 101 +++++
 .pine/tickets/BUG-n9a6bz.md                        |  54 +++
 .pine/tickets/BUG-namghh.md                        |  48 +++
 .pine/tickets/BUG-nbymq4.md                        |  52 +++
 .pine/tickets/BUG-ngt25j.md                        |  52 +++
 .pine/tickets/BUG-nn74ph.md                        |  51 +++
 .pine/tickets/BUG-nzy3pa.md                        | 186 ++++++++
 .pine/tickets/BUG-p334yw.md                        |  53 +++
 .pine/tickets/BUG-p3j233.md                        |  53 +++
 .pine/tickets/BUG-phv0r9.md                        |  56 +++
 .pine/tickets/BUG-ppvyzr.md                        |  58 +++
 .pine/tickets/BUG-pzkpfr.md                        |  53 +++
 .pine/tickets/BUG-q6b75c.md                        |  52 +++
 .pine/tickets/BUG-r1m83f.md                        |  52 +++
 .pine/tickets/BUG-rbask0.md                        | 140 ++++++
 .pine/tickets/BUG-rh7mpa.md                        |  52 +++
 .pine/tickets/BUG-rs0xq1.md                        |  46 ++
 .pine/tickets/BUG-rytwy7.md                        |  55 +++
 .pine/tickets/BUG-sgrxhh.md                        |  53 +++
 .pine/tickets/BUG-t12ffz.md                        |  57 +++
 .pine/tickets/BUG-t3p92b.md                        |  94 ++++
 .pine/tickets/BUG-txafja.md                        |  55 +++
 .pine/tickets/BUG-v8ksv8.md                        |  49 +++
 .pine/tickets/BUG-vsmnby.md                        |  36 ++
 .pine/tickets/BUG-x28fsx.md                        | 142 ++++++
 .pine/tickets/BUG-x6gyc1.md                        |  54 +++
 .pine/tickets/BUG-xam6t8.md                        | 179 ++++++++
 .pine/tickets/BUG-y38bss.md                        |  54 +++
 .pine/tickets/BUG-ywbvfa.md                        |  36 ++
 .pine/tickets/BUG-z0s4zg.md                        | 100 +++++
 .pine/tickets/BUG-zf4pnj.md                        |  55 +++
 .pine/tickets/EPIC-3en6xr.md                       |  88 ++++
 .pine/tickets/EPIC-62zt4j.md                       | 110 +++++
 .pine/tickets/EPIC-7c3ry9.md                       |  44 ++
 .pine/tickets/EPIC-8rbys7.md                       | 192 +++++++++
 .pine/tickets/EPIC-m42s3g.md                       |   2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 478 +++++++++++++++++++++
 .pine/tickets/FEAT-02cj1g.md                       | 102 +++++
 .pine/tickets/FEAT-02zdcq.md                       | 169 ++++++++
 .pine/tickets/FEAT-0hdfzd.md                       | 106 +++++
 .pine/tickets/FEAT-0xsc1s.md                       |  35 ++
 .pine/tickets/FEAT-1ge0xc.md                       |  31 ++
 .pine/tickets/FEAT-1mxtsn.md                       | 104 +++++
 .pine/tickets/FEAT-274c4p.md                       |  68 +++
 .pine/tickets/FEAT-27g2za.md                       |  33 ++
 .pine/tickets/FEAT-2kx0hx.md                       | 260 +++++++++++
 .pine/tickets/FEAT-2m24nh.md                       |  53 +++
 .pine/tickets/FEAT-2m4yvz.md                       | 101 +++++
 .pine/tickets/FEAT-38je8w.md                       |  35 ++
 .pine/tickets/FEAT-39ttf6.md                       |  32 ++
 .pine/tickets/FEAT-3t112f.md                       |  53 +++
 .pine/tickets/FEAT-3ykb4v.md                       |  37 ++
 .pine/tickets/FEAT-4bjfny.md                       | 100 +++++
 .pine/tickets/FEAT-4bvcrb.md                       |  38 ++
 .pine/tickets/FEAT-4e376e.md                       |  56 +++
 .pine/tickets/FEAT-4jhtny.md                       |  30 ++
 .pine/tickets/FEAT-4pz9fn.md                       |  37 ++
 .pine/tickets/FEAT-53pa9a.md                       |  52 +++
 .pine/tickets/FEAT-5fx926.md                       |  57 +++
 .pine/tickets/FEAT-5g42rz.md                       |  30 ++
 .pine/tickets/FEAT-5kv1jq.md                       |   8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |  99 +++++
 .pine/tickets/FEAT-6m295t.md                       |  38 ++
 .pine/tickets/FEAT-6qzza1.md                       |  58 +++
 .pine/tickets/FEAT-6r663e.md                       |  32 ++
 .pine/tickets/FEAT-70j6dn.md                       |  55 +++
 .pine/tickets/FEAT-7cg0cd.md                       |   6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  40 ++
 .pine/tickets/FEAT-7t0xks.md                       |  31 ++
 .pine/tickets/FEAT-8752vx.md                       |  54 +++
 .pine/tickets/FEAT-8zgwp6.md                       |  32 ++
 .pine/tickets/FEAT-9ep5pw.md                       |  31 ++
 .pine/tickets/FEAT-a3dwj2.md                       |  52 +++
 .pine/tickets/FEAT-afkx3k.md                       |  37 ++
 .pine/tickets/FEAT-bfrkyk.md                       |  54 +++
 .pine/tickets/FEAT-c81kp3.md                       |  59 +++
 .pine/tickets/FEAT-cgm1y3.md                       |   2 +-
 .pine/tickets/FEAT-csqgg5.md                       |   6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |  59 +++
 .pine/tickets/FEAT-edzr73.md                       | 121 ++++++
 .pine/tickets/FEAT-egm8bf.md                       |  37 ++
 .pine/tickets/FEAT-eqzpzq.md                       | 136 ++++++
 .pine/tickets/FEAT-ez6xtm.md                       |  55 +++
 .pine/tickets/FEAT-f045nj.md                       | 131 ++++++
 .pine/tickets/FEAT-f3hx3a.md                       |  37 ++
 .pine/tickets/FEAT-fpqg78.md                       |  52 +++
 .pine/tickets/FEAT-fqmh01.md                       |  97 +++++
 .pine/tickets/FEAT-fs3pjr.md                       | 205 +++++++++
 .pine/tickets/FEAT-gzd32h.md                       |  31 ++
 .pine/tickets/FEAT-hxztwz.md                       |  37 ++
 .pine/tickets/FEAT-je4f4t.md                       |   4 +-
 .pine/tickets/FEAT-jwhdsy.md                       |   2 +-
 .pine/tickets/FEAT-kcdrcy.md                       | 130 ++++++
 .pine/tickets/FEAT-kfmq1z.md                       |  53 +++
 .pine/tickets/FEAT-kpn0m3.md                       |  37 ++
 .pine/tickets/FEAT-ktasef.md                       | 103 +++++
 .pine/tickets/FEAT-ky75b5.md                       |  52 +++
 .pine/tickets/FEAT-m1fdn4.md                       |  56 +++
 .pine/tickets/FEAT-m7aw75.md                       |  54 +++
 .pine/tickets/FEAT-mammrz.md                       |  35 ++
 .pine/tickets/FEAT-mccadj.md                       |  38 ++
 .pine/tickets/FEAT-mh4e8g.md                       |  32 ++
 .pine/tickets/FEAT-mj2nek.md                       |  98 +++++
 .pine/tickets/FEAT-mngmn1.md                       |  32 ++
 .pine/tickets/FEAT-mq412g.md                       |  58 +++
 .pine/tickets/FEAT-mxmjt7.md                       | 129 ++++++
 .pine/tickets/FEAT-n010f0.md                       |  33 ++
 .pine/tickets/FEAT-n12211.md                       |  34 ++
 .pine/tickets/FEAT-nch9dg.md                       |   6 +-
 .pine/tickets/FEAT-npc3ge.md                       |  37 ++
 .pine/tickets/FEAT-nq1vsx.md                       |  53 +++
 .pine/tickets/FEAT-p01rcw.md                       |  98 +++++
 .pine/tickets/FEAT-p75n7j.md                       |  38 ++
 .pine/tickets/FEAT-pfwjzk.md                       |  30 ++
 .pine/tickets/FEAT-ppnetz.md                       | 141 ++++++
 .pine/tickets/FEAT-pqnxx4.md                       |  37 ++
 .pine/tickets/FEAT-prw1hw.md                       |  56 +++
 .pine/tickets/FEAT-pt6ge9.md                       |  34 ++
 .pine/tickets/FEAT-pxcbqj.md                       |  39 ++
 .pine/tickets/FEAT-q81bq4.md                       |   2 +-
 .pine/tickets/FEAT-qf0hsa.md                       |  53 +++
 .pine/tickets/FEAT-r267jj.md                       |  35 ++
 .pine/tickets/FEAT-r8ph93.md                       |  38 ++
 .pine/tickets/FEAT-rdfjh1.md                       |  32 ++
 .pine/tickets/FEAT-re138f.md                       |  54 +++
 .pine/tickets/FEAT-rkj8ry.md                       |  37 ++
 .pine/tickets/FEAT-s3sfx5.md                       |  31 ++
 .pine/tickets/FEAT-s99vdp.md                       | 155 +++++++
 .pine/tickets/FEAT-sc3qrq.md                       |  54 +++
 .pine/tickets/FEAT-sz4ddp.md                       |  57 +++
 .pine/tickets/FEAT-t26rt7.md                       |   2 +-
 .pine/tickets/FEAT-t38djq.md                       |  56 +++
 .pine/tickets/FEAT-t58m89.md                       |  32 ++
 .pine/tickets/FEAT-t672pv.md                       |  57 +++
 .pine/tickets/FEAT-tjcr13.md                       |  52 +++
 .pine/tickets/FEAT-v2nenc.md                       |  58 +++
 .pine/tickets/FEAT-vjjs8t.md                       |  36 ++
 .pine/tickets/FEAT-vntngh.md                       |  64 +++
 .pine/tickets/FEAT-vvwpjw.md                       |   2 +-
 .pine/tickets/FEAT-w7n7x6.md                       | 131 ++++++
 .pine/tickets/FEAT-w9kqeg.md                       |  10 +-
 .pine/tickets/FEAT-wcr6en.md                       |  52 +++
 .pine/tickets/FEAT-wzfz3d.md                       |  59 +++
 .pine/tickets/FEAT-x9gq0s.md                       |  37 ++
 .pine/tickets/FEAT-xj5tv6.md                       |  38 ++
 .pine/tickets/FEAT-xr75b9.md                       |  58 +++
 .pine/tickets/FEAT-xzdn35.md                       |  56 +++
 .pine/tickets/FEAT-ybm2pd.md                       |   2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |  32 ++
 .pine/tickets/FEAT-ys734v.md                       |  36 ++
 .pine/tickets/FEAT-yxhgeh.md                       |  38 ++
 .pine/tickets/FEAT-yyjfjq.md                       |   2 +-
 .pine/tickets/FEAT-z90r5a.md                       |  32 ++
 .pine/tickets/FEAT-zhdxc4.md                       |  38 ++
 .pine/tickets/FEAT-zjrw76.md                       |  37 ++
 .pine/tickets/FEAT-zm3wh2.md                       |  99 +++++
 .pine/tickets/FEAT-zn5rqy.md                       | 103 +++++
 .pine/tickets/FEAT-zwpvbf.md                       |  60 +++
 CHANGELOG.md                                       |  25 ++
 CONTRIBUTING.md                                    |  22 +
 README.md                                          | 450 +++++--------------
 cmd/kilasflow/main.go                              |  19 +-
 config.example.yaml                                |   8 +-
 docs/src/content/docs/concepts/architecture.md     |  84 ++++
 docs/src/content/docs/concepts/execution-model.md  |  15 +-
 docs/src/content/docs/concepts/node-registry.md    |   5 +-
 .../src/content/docs/concepts/safety-boundaries.md |   2 +-
 docs/src/content/docs/concepts/webhooks.md         |  33 ++
 .../content/docs/operate/acceptance-capstone.md    |   3 +-
 .../docs/operate/configuration-reference.md        |   8 +-
 docs/src/content/docs/reference/api-contract.md    |  15 +-
 docs/src/content/docs/reference/api.md             |   2 +-
 docs/src/content/docs/reference/api/events.md      |  13 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |  15 +-
 e2e/helpers/seed.ts                                |  15 +
 e2e/tests/editor-chat.spec.ts                      |  68 ++-
 gflow-prd-v1.md                                    |   8 +-
 internal/ai/openai.go                              |  77 +++-
 internal/ai/openai_test.go                         |  66 +++
 internal/api/datastores_test.go                    |  29 ++
 internal/api/handlers/datastores.go                |   7 +
 internal/api/handlers/executions.go                |  46 +-
 internal/api/handlers/executions_events_test.go    |  64 +++
 internal/config/config.go                          |  10 +-
 internal/config/config_test.go                     |  22 +
 .../datastore_unique_names_migration_test.go       | 176 ++++++++
 internal/database/workflow_actor_migration_test.go |   7 +-
 internal/datastore/catalogue.go                    |  46 +-
 internal/datastore/engine.go                       |  26 +-
 internal/datastore/engine_test.go                  |  13 +-
 internal/datastore/names.go                        | 132 ++++++
 internal/datastore/names_test.go                   | 271 ++++++++++++
 internal/embed/confinement.go                      |  12 -
 internal/embed/confinement_test.go                 |  14 +-
 internal/engine/approval.go                        |   2 +-
 internal/loadoptions/datastores.go                 |  31 +-
 internal/loadoptions/datastores_test.go            |  35 ++
 .../000021_datastore_unique_names.down.sql         |   8 +
 .../postgres/000021_datastore_unique_names.up.sql  |  47 ++
 .../sqlite/000021_datastore_unique_names.down.sql  |   8 +
 .../sqlite/000021_datastore_unique_names.up.sql    |  43 ++
 nodes/ai.go                                        |  75 ++--
 nodes/ai_test.go                                   |  59 +++
 nodes/datastore.go                                 |  23 +-
 nodes/datastore_byname_test.go                     |  76 ++++
 nodes/embedscope.go                                |  26 +-
 nodes/embedscope_test.go                           |  31 ++
 scripts/generate-api-reference.mjs                 |  28 +-
 sdk/src/generated/models.ts                        | 250 +++++++++++
 sidecar/runner_test.go                             |  15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   2 +-
 web/messages/en/editor.json                        |  20 +-
 web/messages/id/editor.json                        |  20 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIAgentFailedEvent.ts |  24 ++
 .../api/generated/models/aIModelCompletedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |  24 ++
 .../api/generated/models/aIModelStartedEvent.ts    |  24 ++
 .../api/generated/models/aIToolCompletedEvent.ts   |  24 ++
 .../lib/api/generated/models/aIToolFailedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIToolStartedEvent.ts |  24 ++
 web/src/lib/api/generated/models/index.ts          |  10 +
 web/src/lib/api/generated/models/otherEvent.ts     |  24 ++
 .../models/streamExecutionEvents200Item.ts         |  90 ++++
 .../api/generated/models/webhookResponseEvent.ts   |  24 ++
 .../workflow-editor/canvas-chat-panel.svelte       | 381 +++++++++++++---
 .../workflow-editor/chat-markdown.svelte           |  38 ++
 .../workflow-editor/workflow-editor.svelte         |  54 ++-
 web/src/lib/workflow-editor/chat-markdown.test.ts  | 110 +++++
 web/src/lib/workflow-editor/chat-markdown.ts       | 211 +++++++++
 web/src/lib/workflow-editor/chat-stream.test.ts    |  70 +++
 web/src/lib/workflow-editor/chat-stream.ts         | 112 +++++
 web/src/lib/workflow-editor/chat.test.ts           |  54 ++-
 web/src/lib/workflow-editor/chat.ts                |  78 +++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |  44 +-
 .../lib/workflow-editor/execution-watch.test.ts    |  58 +++
 web/src/lib/workflow-editor/execution-watch.ts     |  56 +++
 web/src/lib/workflow-editor/validation.ts          |   5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  36 +-
 274 files changed, 15830 insertions(+), 612 deletions(-)
```
