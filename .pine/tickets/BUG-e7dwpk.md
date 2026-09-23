---
id: BUG-e7dwpk
title: Datastore names can collide (incl. case-only); 'By Name' silently acts on the wrong table
status: doing
priority: high
labels:
    - datastore
    - data-integrity
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T04:08:40Z"
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

# Attachments
