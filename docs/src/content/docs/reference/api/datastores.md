---
title: Datastores
description: "Tenant-owned row stores: tables, columns, rows, and CSV import and export. Generated from the live OpenAPI document."
sidebar:
  order: 7
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Tenant-owned row stores: tables, columns, rows, and CSV import and export.

## List datastores (`list-datastores`)

`GET /api/v1/datastores`

Returns one page of data tables, in name order. The next page's cursor is in the X-Next-Cursor response header, empty on the last page.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `limit` | query | no | integer | Maximum datastores to return (default 100) |
| `cursor` | query | no | string | Opaque cursor from the previous page's X-Next-Cursor header |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `DatastoreListOutputBody` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Create a datastore (`create-datastore`)

`POST /api/v1/datastores`

Creates a data table; columns are added afterwards.

Request body: `application/json` — `CreateDatastoreInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `DatastoreResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Get a datastore (`get-datastore`)

`GET /api/v1/datastores/{id}`

Returns one data table with its columns.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `DatastoreResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Rename a datastore (`rename-datastore`)

`PUT /api/v1/datastores/{id}`

Renames a data table; columns change through the column endpoints.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `application/json` — `RenameDatastoreInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `DatastoreResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Delete a datastore (`delete-datastore`)

`DELETE /api/v1/datastores/{id}`

Removes a data table and every row it holds.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `204` | No Content |  |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Add a column (`add-datastore-column`)

`POST /api/v1/datastores/{id}/columns`

Appends one user column to a data table.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `application/json` — `DatastoreColumnInput` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `DatastoreColumnResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Rename a column (`rename-datastore-column`)

`PUT /api/v1/datastores/{id}/columns/{name}`

Renames one user column of a data table.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |
| `name` | path | yes | string | Column name |

Request body: `application/json` — `RenameDatastoreColumnInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `DatastoreColumnResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Delete a column (`delete-datastore-column`)

`DELETE /api/v1/datastores/{id}/columns/{name}`

Removes one user column from a data table.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |
| `name` | path | yes | string | Column name |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `204` | No Content |  |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Insert a row (`insert-datastore-row`)

`POST /api/v1/datastores/{id}/rows`

Writes one row and reads it back. Send Idempotency-Key to make a retry safe: 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |
| `Idempotency-Key` | header | no | string | 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency. |

Request body: `application/json` — `InsertRowInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `object` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Get a row (`get-datastore-row`)

`GET /api/v1/datastores/{id}/rows/{rowId}`

Returns one row by id.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |
| `rowId` | path | yes | integer | Row identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `object` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## List rows (`list-datastore-rows`)

`GET /api/v1/datastores/{id}/rows`

Returns one page of rows in id order with the cursor for the next.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |
| `limit` | query | no | integer | Page size; clamped to the store maximum |
| `cursor` | query | no | string | Opaque cursor from a previous page |
| `match` | query | no | string | Any Condition matches any, All Conditions matches all; default Any Condition |
| `columnName` | query | no | array or null | Filter column, repeated; zipped with condition and value by position |
| `condition` | query | no | array or null | Filter operator, repeated |
| `value` | query | no | array or null | Filter value as JSON, repeated; unquoted text stays a string |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `RowListOutputBody` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Update rows (`update-datastore-rows`)

`PUT /api/v1/datastores/{id}/rows`

Sets columns on every row matching the filter. One statement, atomic per row on both drivers: concurrent writers never interleave inside a row and the last writer wins; no row lock is taken.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `application/json` — `UpdateRowsInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `UpdateRowsOutputBody` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Upsert rows (`upsert-datastore-row`)

`POST /api/v1/datastores/{id}/rows/upsert`

Updates every row matching the filter, or inserts one row when nothing matches. Read-then-write in no single transaction: two concurrent upserts against the same filter may both insert, so a counter that must not lose writes uses increment instead. Send Idempotency-Key to make a retry safe: 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |
| `Idempotency-Key` | header | no | string | 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency. |

Request body: `application/json` — `UpsertRowInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `UpsertRowOutputBody` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Delete rows (`delete-datastore-rows`)

`DELETE /api/v1/datastores/{id}/rows`

Removes every row matching the filter. An empty filter is refused and removes nothing. One statement, atomic per row on both drivers: the last writer wins and no row lock is taken.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `application/json` — `DeleteRowsInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `DeleteRowsOutputBody` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Clear a datastore (`clear-datastore`)

`POST /api/v1/datastores/{id}/clear`

Removes every row and keeps the schema.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ClearedDatastoreOutputBody` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Import rows from CSV (`import-datastore-rows`)

`POST /api/v1/datastores/{id}/rows/import`

Validates every record before writing any row: a file with a failed row imports nothing and reports each failure with its line number.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `text/csv` — `string` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `CSVImportReport` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Export rows as CSV (`export-datastore-rows`)

`GET /api/v1/datastores/{id}/rows/export`

Streams the datastore's rows as RFC 4180 CSV in id order, one header row plus one record per row.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |
| `includeSystemColumns` | query | no | boolean | Include the id, createdAt and updatedAt columns |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | The datastore's rows as RFC 4180 CSV. | `text/csv` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
