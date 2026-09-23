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

Closed by `pine close --evidence` on 2026-09-23. Rewritten on 2026-09-23 to list only this ticket's own commits (`git log --grep "BUG-e7dwpk" 2f8e9b1..5830b4e`) and the files exactly those commits changed.

- Range: `2f8e9b1..5830b4e`
- Commits (4):
  - `85c9afaa` — BUG-e7dwpk: a taken data table name answers 409 whatever it says, never 404
  - `fd21d80d` — BUG-e7dwpk: migration 000021 groups duplicate data table names the way the engine compares them, trimmed as well as folded
  - `b0a1dbe9` — chore(pine): close BUG-e7dwpk with its landing evidence
  - `46a52682` — BUG-e7dwpk: a data table's name belongs to one table in its tenant, and By Name refuses a name it cannot pin to one
- Merged by (1):
  - `fa15f1ed` — merge: a data table's name is unique in its tenant, and a Data table Tool performs the write it is set to with the model supplying values only (BUG-e7dwpk, BUG-t12ffz)
- Files changed by those commits (merge commits excluded):

```
 .pine/tickets/BUG-e7dwpk.md                        | 326 ++++++++++++++++++++-
 cmd/kilasflow/main.go                              |  19 +-
 internal/api/datastores_test.go                    |  57 ++++
 internal/api/handlers/datastores.go                |  27 ++-
 .../datastore_unique_names_migration_test.go       | 210 +++++++++++++
 internal/database/workflow_actor_migration_test.go |   7 +-
 internal/datastore/catalogue.go                    |  46 +++-
 internal/datastore/engine.go                       |  26 ++-
 internal/datastore/engine_test.go                  |  13 +-
 internal/datastore/names.go                        | 132 +++++++++
 internal/datastore/names_test.go                   | 271 +++++++++++++++++
 internal/embed/confinement.go                      |  12 -
 internal/embed/confinement_test.go                 |  14 +-
 internal/loadoptions/datastores.go                 |  31 ++-
 internal/loadoptions/datastores_test.go            |  35 +++
 .../000021_datastore_unique_names.down.sql         |   8 +
 .../postgres/000021_datastore_unique_names.up.sql  |  67 ++++-
 .../sqlite/000021_datastore_unique_names.down.sql  |   8 +
 .../sqlite/000021_datastore_unique_names.up.sql    |  63 ++++-
 nodes/datastore.go                                 |  23 +-
 nodes/datastore_byname_test.go                     |  76 +++++
 nodes/embedscope.go                                |  26 ++-
 nodes/embedscope_test.go                           |  31 ++
 23 files changed, 1433 insertions(+), 95 deletions(-)
```
