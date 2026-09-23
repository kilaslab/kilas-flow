---
name: kilasflow-datastore
description: Use when reading or writing a KilasFlow data table, listing datastores or their columns, filtering which rows a call acts on, or moving rows as CSV. Triggers on "datastore", "table", "columns", "rows", "filter", "CSV", "limits".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow context
  - kilasflow datastore list
  - kilasflow datastore get
  - kilasflow datastore rows
  - kilasflow datastore export
  - kilasflow api
kilasflow_operations:
  - list-datastores
  - get-datastore
  - list-datastore-rows
  - get-datastore-row
  - export-datastore-rows
  - import-datastore-rows
  - insert-datastore-row
  - update-datastore-rows
  - delete-datastore-rows
  - upsert-datastore-row
  - increment-datastore-rows
  - create-datastore
  - rename-datastore
  - delete-datastore
  - add-datastore-column
  - rename-datastore-column
  - delete-datastore-column
  - clear-datastore
kilasflow_nodes:
  - kilasflow.datastore
  - kilasflow.datastoreTool
kilasflow_expression_roots:
  - $json
kilasflow_not_shipped:
  - 'No guarded write verbs: credential create, update and delete, datastore create, rename, delete, columns and clear, schedule create, update and delete, pack install, and tenant create and delete have no verbs yet'
  - 'No idempotency flag: the server honours Idempotency-Key on a run and on a row write, but no verb carries the key yet'
  - 'No storage substitution for the datastore: one database serves every tenant, and there is no interface a host implements'
---

## Non-negotiables

1. Read the schema before you write a row. `kilasflow datastore get <datastoreId>` (`get-datastore`) is the only thing that says which columns exist and what type each holds; a write naming anything else is refused, and nothing creates a column on your way in (internal/api/handlers/datastores.go, internal/datastore/idents.go).
2. There is no verb for a row write, a schema change or a CSV import. Reach those through `kilasflow api <operation-id>` (`api`) with `--path`, `--body` and `--header`; the four read verbs are the whole datastore CLI (internal/cli/verbs_datastore.go).
3. Never send an empty filter to a write. An update, delete, upsert or increment with no condition is refused 422: an empty filter matches nothing by refusal, never the whole table, because one that compiled to no predicate would rewrite or delete every row and answer 200 (internal/api/handlers/datastores.go, `refuseEmptyFilter`).
4. A counter is `increment-datastore-rows`, never read-then-write. A value you got from `get-datastore-row` and wrote back with `update-datastore-rows` is a lost update waiting for the second worker; the increment adds in one statement and returns the value its own statement produced (internal/datastore/concurrency.go).

## Strong defaults

- Open with `kilasflow context`: server, identity, workflows, datastores and node types in one read-only call.
- Enumerate tables with `kilasflow datastore list` (`list-datastores`, `--limit` 1-500 defaulting to 100 and `--cursor` from the previous page); then `kilasflow datastore get <datastoreId>` (`get-datastore`) for `id`, `name` and `columns[{name,type}]`, where a type is one of `string`, `number`, `boolean` or `date` (internal/api/handlers/datastores.go).
- Read rows with `kilasflow datastore rows <datastoreId>` (`list-datastore-rows`, `--limit` defaulting to 25 and clamped at 100, `--cursor` to keep paging) (internal/datastore/filter.go). The filter query language is not exposed on this verb: use `kilasflow api list-datastore-rows --path id=<datastoreId> --query match=all --query columnName=status --query condition=eq --query value=active`, and read references/FILTERS.md before writing one.
- Read one row back with `kilasflow api get-datastore-row --path id=<datastoreId> --path rowId=<rowId>` (`get-datastore-row`); a write answers with the rows it wrote, so this is for re-reading later, not for verifying the last call.
- Write one call at a time through the escape hatch: `insert-datastore-row` (201 with a `Location`), `update-datastore-rows` and `delete-datastore-rows` (both answer `matched`/`deleted` plus the rows), `upsert-datastore-row` (answers `inserted`, so you can see which branch ran) and `increment-datastore-rows` (a number column, `amount` defaulting to 1, an empty cell counting as zero).
- Make a retried insert or upsert safe: `--header Idempotency-Key=<key>` on `insert-datastore-row` or `upsert-datastore-row` replays the first outcome with no second row and marks the response `Idempotent-Replayed: true`; the same key against a different request or resource is 409 (internal/api/handlers/datastores.go, `internal/idempotency/**`).
- Make a write conditional on a stamp a read returned: `ifUpdatedAt` on `update-datastore-rows` and `delete-datastore-rows` turns it into compare-and-swap on that row's `updatedAt`, must match exactly one row, and answers 409 with the row's current stamp when it moved (internal/datastore/concurrency.go; internal/api/handlers/datastores.go).
- Move rows as CSV: `kilasflow datastore export <datastoreId>` (`export-datastore-rows`) streams RFC 4180 CSV to stdout, `--out rows.csv` writes the file 0600 and reports `path` and `bytes`, and the API's `includeSystemColumns` adds `id`, `createdAt` and `updatedAt` for a backup rather than for a sheet somebody will edit (internal/cli/verbs_datastore.go, internal/api/handlers/datastores_csv.go).
- Import CSV with `kilasflow api import-datastore-rows --path id=<datastoreId> --header Content-Type=text/csv --body @rows.csv`: the body is a raw `text/csv` file, not JSON, and every record is validated before any row is written (internal/cli/client.go, internal/api/handlers/datastores_csv.go).
- Change the schema through the escape hatch: `create-datastore` (`name`, optionally `columns[{name,type}]`), `rename-datastore`, `add-datastore-column`, `rename-datastore-column`, `delete-datastore-column`, `clear-datastore` (removes every row and keeps the schema) and `delete-datastore` (204). A column name is 1-63 bytes matching `^[a-zA-Z][a-zA-Z0-9_]*$`, and `id`, `createdAt`, `updatedAt` and `dryRunState` are reserved case-insensitively (internal/datastore/idents.go).
- Inside a workflow use the node rather than the API: `kilasflow.datastore` carries the row operations `insert`, `get`, `update`, `upsert`, `increment`, `delete`, `ifExists` and `ifNotExists` and the table operations `create`, `list`, `rename`, `deleteTable` and `clear`. Its `dataTableId` locator resolves By-ID or By-Name in the tenant's own catalogue, `columns` is the resource mapper (`Values to Send`, automatic mapping intersects the item's keys with the live schema), `match` is Any/All Conditions, and `filters.conditions[i]` is the `keyName`/`condition`/`keyValue` triple. Only the value slots are expression-capable (`keyValue` supports `$json`, and so does a mapped value); `keyName` and `counterColumn` are column names and never expressions (nodes/datastore.go).
- `kilasflow.datastoreTool` is the agent tool: it binds one table and performs one of `get` (the default), `insert`, `update`, `upsert` or `delete`. A `get` freezes that table's columns into a closed schema, answers filtered reads from the fixed ten-operator vocabulary, and caps a call at 50 rows by default, 200 rows and 256 KiB. A write takes its values from the `$fromAI` calls in its parameters, which are its whole schema, runs through the node's own write rules (a matching write needs a condition, and `$fromAI` may fill only a mapped column's value or a condition's value, never the write's structure), hands the model's values to an expression as data it computes with and never as code it runs (a `$fromAI` key must be declared one way everywhere), and answers `operation`, `affected` and the rows. An `insert` that maps no column reads instead, which is what keeps tools built before writes existed reading; an `update` or `upsert` that maps none is refused (nodes/datastore.go).
- Plan against the bounds, which refuse a write and never evict a row: 100 datastores per tenant, 100 columns per datastore, 100,000 rows per datastore and 1 MiB per value (internal/datastore/limits.go).

## Decision tree

```
what are you doing?
|
+-- discovering
|     -> kilasflow datastore list
|        then kilasflow datastore get <datastoreId> for the columns
|
+-- reading rows
|     -> kilasflow datastore rows <datastoreId> --limit 50 --cursor <cursor>
|        with a filter: kilasflow api list-datastore-rows --path id=<datastoreId> \
|                          --query match=all --query columnName=status \
|                          --query condition=eq --query value=active
|        the grammar is references/FILTERS.md
|
+-- writing rows (no verb)
|     -> kilasflow api insert-datastore-row --path id=<datastoreId> --body @row.json
|        update/delete: the filter is mandatory and non-empty
|        increment for a counter, upsert for create-or-update
|
+-- a bulk load
|     -> kilasflow api import-datastore-rows --path id=<datastoreId> \
|              --header Content-Type=text/csv --body @rows.csv
|        one bad record imports nothing and names its line
|
+-- a spreadsheet out
|     -> kilasflow datastore export <datastoreId> > rows.csv
|
+-- changing the schema
|     -> add-datastore-column / rename-datastore-column / delete-datastore-column,
|        create-datastore, rename-datastore, clear-datastore, delete-datastore
|
+-- refused 409 on a conditional write
|     -> the row moved after you read it: re-read, re-apply against the new stamp
|
+-- refused 422
      -> a name, a type, a filter or a limit the store will not take:
         the message names what to change, and references/FILTERS.md has the filter cases
```

## Not shipped yet

- No guarded write verbs: credential create, update and delete, datastore create, rename, delete, columns and clear, schedule create, update and delete, pack install, and tenant create and delete have no verbs yet — the operations are served, so reach each one with `kilasflow api <operation-id>` and `--path`/`--body` until a verb exists for it.
- No idempotency flag: the server honours Idempotency-Key on a run and on a row write, but no verb carries the key yet — pass it yourself with `kilasflow api insert-datastore-row --header Idempotency-Key=<key>` (and the same on `upsert-datastore-row`), and treat a write you cannot key as one that a retry performs twice.
- No storage substitution for the datastore: one database serves every tenant, and there is no interface a host implements — size the server's database for the tables the tenant writes, and treat a datastore as tenant data that lives in that database rather than in a store the host supplies.

## Anti-patterns

- "I remember the columns" → the write is refused, or worse it writes the columns that did exist and drops the rest of your intent → `kilasflow datastore get <datastoreId>` first and build the `values` object from what it returned.
- "I'll just update the counter I read" → two workers read the same value and the second write erases the first → `increment-datastore-rows` with the same filter, which adds in one statement and answers the value its own statement produced.
- "Delete with an empty filter to clear it" → refused 422 by design, because the version that matched everything would empty a table on a 200 → `clear-datastore` to remove every row deliberately, or a filter with at least one condition.
- "The upsert matched on `email`, so two workers cannot both insert" → only an upsert addressed by `id` is a single statement; any other column is read-then-write with no shared transaction → give the row its own id, or accept that a loser of the race needs to be reconciled.
- "CSV import returned a body, so the rows landed" → a file with one failed record writes nothing and reports each failure by line → read `inserted`, `skipped` and `failed[]`, fix the named lines and send the file again.
- "It exported to stdout, so the CSV is safe" → a cell a spreadsheet would evaluate is prefixed with a quote on export and unquoted on import, and the file lands 0600 only when you pass `--out` → use `--out <path>` for anything kept, and never open a tenant's export in a sheet you trust.
- "One big `datastore rows` call will read it all" → the page is 25 rows by default and clamps at 100, so the rest is silently absent → follow `--cursor` until it is empty, or `export-datastore-rows` when the whole table is what you want.
- "I'll raise the limit for a bigger read" → the row listing clamps, and the datastore's own caps refuse a huge write rather than evicting rows → page with the cursor, and read the refusal's numbers instead of retrying.

## Reference files

| File | Read when |
| --- | --- |
| FILTERS.md | you are writing a filter for a read, update, delete, upsert or increment, or you need the operator list, value coercion, match mode or paging rules |
