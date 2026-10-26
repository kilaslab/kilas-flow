---
title: Datastores
description: "Tenant-owned row stores: tables, columns, rows, and CSV import and export. Generated from the live OpenAPI document."
sidebar:
  order: 8
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

Fields of the `DatastoreListOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `items` | yes | `array or null` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "items": [
    {
      "columns": [
        {
          "name": "string",
          "type": "string"
        }
      ],
      "id": "string",
      "name": "string"
    }
  ]
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/datastores" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — the listing names every table in the tenant, and a datastore session is confined to one.

## Create a datastore (`create-datastore`)

`POST /api/v1/datastores`

Creates a data table; columns are added afterwards.

Request body: `application/json` — `CreateDatastoreInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `columns` | no | `array or null` | User columns; empty means system columns only |
| `name` | yes | `string` | Display name |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `DatastoreResource` |
| `default` | Error | `application/problem+json` |

Fields of the `DatastoreResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `columns` | yes | `array or null` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "name": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "columns": [
    {
      "name": "string",
      "type": "string"
    }
  ],
  "id": "string",
  "name": "string"
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/datastores" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"name":"string"}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const datastore = await client.createDatastore({
  name: "orders",
  columns: [{ name: "email", type: "string" }],
});
```

```go title="Go"
payload, _ := json.Marshal(map[string]any{"name": "orders", "columns": []map[string]string{{"name": "email", "type": "string"}}})
resp, err := http.Post(baseURL+"/api/v1/datastores", "application/json", bytes.NewReader(payload))
```

:::

Embed: Deny — creating a table is schema work, outside any session’s authority.

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

Fields of the `DatastoreResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `columns` | yes | `array or null` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "columns": [
    {
      "name": "string",
      "type": "string"
    }
  ],
  "id": "string",
  "name": "string"
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/datastores/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `datastore:read` — on the session’s own datastore only; another datastore’s id reads as `404`, not `403`, so a session cannot probe ids.

## Rename a datastore (`rename-datastore`)

`PUT /api/v1/datastores/{id}`

Renames a data table; columns change through the column endpoints.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `application/json` — `RenameDatastoreInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `name` | yes | `string` | New display name |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `DatastoreResource` |
| `default` | Error | `application/problem+json` |

Fields of the `DatastoreResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `columns` | yes | `array or null` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "name": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "columns": [
    {
      "name": "string",
      "type": "string"
    }
  ],
  "id": "string",
  "name": "string"
}
```

```sh
curl -X PUT "$KILASFLOW_HOST/api/v1/datastores/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"name":"string"}'
```

Embed: Deny — renaming or dropping a table is schema work, not contents.

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

```sh
curl -X DELETE "$KILASFLOW_HOST/api/v1/datastores/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — renaming or dropping a table is schema work, not contents.

## Add a column (`add-datastore-column`)

`POST /api/v1/datastores/{id}/columns`

Appends one user column to a data table.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `application/json` — `DatastoreColumnInput` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `name` | yes | `string` | Column name |
| `type` | yes | `string` | Column type: string, number, boolean or date |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `DatastoreColumnResource` |
| `default` | Error | `application/problem+json` |

Fields of the `DatastoreColumnResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `name` | yes | `string` | — |
| `type` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "name": "string",
  "type": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "name": "string",
  "type": "string"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/datastores/{id}/columns" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"name":"string","type":"string"}'
```

Embed: Deny — clearing every row and editing columns reshape the table itself, not its contents.

## Rename a column (`rename-datastore-column`)

`PUT /api/v1/datastores/{id}/columns/{name}`

Renames one user column of a data table.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |
| `name` | path | yes | string | Column name |

Request body: `application/json` — `RenameDatastoreColumnInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `name` | yes | `string` | New column name |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `DatastoreColumnResource` |
| `default` | Error | `application/problem+json` |

Fields of the `DatastoreColumnResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `name` | yes | `string` | — |
| `type` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "name": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "name": "string",
  "type": "string"
}
```

```sh
curl -X PUT "$KILASFLOW_HOST/api/v1/datastores/{id}/columns/{name}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"name":"string"}'
```

Embed: Deny — clearing every row and editing columns reshape the table itself, not its contents.

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

```sh
curl -X DELETE "$KILASFLOW_HOST/api/v1/datastores/{id}/columns/{name}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — clearing every row and editing columns reshape the table itself, not its contents.

## Insert a row (`insert-datastore-row`)

`POST /api/v1/datastores/{id}/rows`

Writes one row and reads it back. Send Idempotency-Key to make a retry safe: 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |
| `Idempotency-Key` | header | no | string | 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency. |

Request body: `application/json` — `InsertRowInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `values` | yes | `object` | User column values keyed by column name |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `object` |
| `default` | Error | `application/problem+json` |

Request (minimal — required fields only):

```json
{
  "values": {}
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/datastores/{id}/rows" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"values":{}}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const row = await client.insertDatastoreRow(datastoreId, { email: "user@example.com" },
  { idempotencyKey: crypto.randomUUID() });
```

```go title="Go"
payload, _ := json.Marshal(map[string]string{"email": "user@example.com"})
req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/datastores/"+url.PathEscape(datastoreId)+"/rows", bytes.NewReader(payload))
req.Header.Set("Idempotency-Key", idempotencyKey)
resp, err := http.DefaultClient.Do(req)
```

:::

Embed: Allow with `datastore:write` — contents of the session’s own datastore: insert, upsert, increment, filtered update and delete, and the CSV import.

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

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/datastores/{id}/rows/{rowId}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `datastore:read` — rows of the session’s own datastore; the CSV export included.

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

Fields of the `RowListOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `items` | yes | `array or null` | — |
| `nextCursor` | no | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "items": [
    {}
  ],
  "nextCursor": "string"
}
```

:::code

```sh title="curl"
curl -X GET "$KILASFLOW_HOST/api/v1/datastores/{id}/rows" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
for await (const row of client.iterateDatastoreRows(datastoreId)) {
  console.log(row);
}
```

```go title="Go"
resp, err := http.Get(baseURL + "/api/v1/datastores/" + url.PathEscape(datastoreId) + "/rows?limit=100")
```

:::

Embed: Allow with `datastore:read` — rows of the session’s own datastore; the CSV export included.

## Update rows (`update-datastore-rows`)

`PUT /api/v1/datastores/{id}/rows`

Sets columns on every row matching the filter. One statement, atomic per row on both drivers: concurrent writers never interleave inside a row and the last writer wins; no row lock is taken. Pass ifUpdatedAt — a row's updatedAt exactly as a previous read returned it — to make the write conditional: the filter must then match exactly one row and the write lands only if the row is unchanged. A stale stamp answers 409 with the row's current updatedAt in errors[0].value, so the caller retries against the new stamp without a second read.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `application/json` — `UpdateRowsInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `filter` | yes | `Filter` | Rows to update; an empty filter is refused |
| `filter.filters` | yes | `array or null` | — |
| `filter.type` | yes | `string` | — |
| `ifUpdatedAt` | no | `string` | A row's updatedAt exactly as a previous read returned it; when present the filter must match exactly one row and the write lands only if that row is unchanged |
| `values` | yes | `object` | Columns to set |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `UpdateRowsOutputBody` |
| `default` | Error | `application/problem+json` |

Fields of the `UpdateRowsOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `matched` | yes | `integer` | — |
| `rows` | yes | `array or null` | — |

Request (minimal — required fields only):

```json
{
  "filter": {
    "filters": [
      {
        "columnName": "string",
        "condition": "string",
        "value": null
      }
    ],
    "type": "string"
  },
  "values": {}
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "matched": 0,
  "rows": [
    {}
  ]
}
```

```sh
curl -X PUT "$KILASFLOW_HOST/api/v1/datastores/{id}/rows" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"filter":{"filters":[{"columnName":"string","condition":"string","value":null}],"type":"string"},"values":{}}'
```

Embed: Allow with `datastore:write` — contents of the session’s own datastore: insert, upsert, increment, filtered update and delete, and the CSV import.

## Upsert rows (`upsert-datastore-row`)

`POST /api/v1/datastores/{id}/rows/upsert`

Updates every row matching the filter, or inserts one row when nothing matches. When the filter is exactly one condition, id equals a value between 1 and 9007199254740991, this is a single INSERT ... ON CONFLICT statement on both drivers: it never inserts the same id twice and a missing id is created at exactly that id. Matched on any other column it is read-then-write in no single transaction, so two concurrent upserts against the same filter may both insert. A counter or flag that must not lose writes uses increment. Send Idempotency-Key to make a retry safe: 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |
| `Idempotency-Key` | header | no | string | 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency. |

Request body: `application/json` — `UpsertRowInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `filter` | yes | `Filter` | Rows to match; on no match one row is inserted |
| `filter.filters` | yes | `array or null` | — |
| `filter.type` | yes | `string` | — |
| `values` | yes | `object` | Columns to set or to insert with |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `UpsertRowOutputBody` |
| `default` | Error | `application/problem+json` |

Fields of the `UpsertRowOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `inserted` | yes | `boolean` | — |
| `matched` | yes | `integer` | — |
| `rows` | yes | `array or null` | — |

Request (minimal — required fields only):

```json
{
  "filter": {
    "filters": [
      {
        "columnName": "string",
        "condition": "string",
        "value": null
      }
    ],
    "type": "string"
  },
  "values": {}
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "inserted": false,
  "matched": 0,
  "rows": [
    {}
  ]
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/datastores/{id}/rows/upsert" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"filter":{"filters":[{"columnName":"string","condition":"string","value":null}],"type":"string"},"values":{}}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const result = await client.upsertDatastoreRow(datastoreId,
  datastoreFilter("and", [{ column: "email", operator: "eq", value: "user@example.com" }]),
  { email: "user@example.com", plan: "pro" },
  { idempotencyKey: crypto.randomUUID() });
```

```go title="Go"
payload, _ := json.Marshal(map[string]any{"filter": map[string]any{"type": "and", "filters": []any{map[string]any{"column": "email", "operator": "eq", "value": "user@example.com"}}}, "values": map[string]string{"email": "user@example.com", "plan": "pro"}})
req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/datastores/"+url.PathEscape(datastoreId)+"/rows/upsert", bytes.NewReader(payload))
req.Header.Set("Idempotency-Key", idempotencyKey)
resp, err := http.DefaultClient.Do(req)
```

:::

Embed: Allow with `datastore:write` — contents of the session’s own datastore: insert, upsert, increment, filtered update and delete, and the CSV import.

## Increment rows (`increment-datastore-rows`)

`POST /api/v1/datastores/{id}/rows/increment`

Adds amount (default 1, may be negative) to a number column on every matching row in one statement, atomic per row on both drivers, and returns each row as that statement left it. A NULL cell counts as zero. Concurrent increments never lose a write.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `application/json` — `IncrementRowsInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `amount` | no | `number` | Added to the column in one statement; negative subtracts; absent means 1 |
| `column` | yes | `string` | The number column to add to |
| `filter` | yes | `Filter` | Rows to increment; an empty filter is refused |
| `filter.filters` | yes | `array or null` | — |
| `filter.type` | yes | `string` | — |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `IncrementRowsOutputBody` |
| `default` | Error | `application/problem+json` |

Fields of the `IncrementRowsOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `matched` | yes | `integer` | — |
| `rows` | yes | `array or null` | — |

Request (minimal — required fields only):

```json
{
  "column": "string",
  "filter": {
    "filters": [
      {
        "columnName": "string",
        "condition": "string",
        "value": null
      }
    ],
    "type": "string"
  }
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "matched": 0,
  "rows": [
    {}
  ]
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/datastores/{id}/rows/increment" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"column":"string","filter":{"filters":[{"columnName":"string","condition":"string","value":null}],"type":"string"}}'
```

Embed: Allow with `datastore:write` — contents of the session’s own datastore: insert, upsert, increment, filtered update and delete, and the CSV import.

## Delete rows (`delete-datastore-rows`)

`DELETE /api/v1/datastores/{id}/rows`

Removes every row matching the filter. An empty filter is refused and removes nothing. One statement, atomic per row on both drivers: the last writer wins and no row lock is taken. Pass ifUpdatedAt — a row's updatedAt exactly as a previous read returned it — to make the delete conditional: the filter must then match exactly one row and the delete lands only if the row is unchanged. A stale stamp answers 409 with the row's current updatedAt in errors[0].value, so the caller retries against the new stamp without a second read.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `application/json` — `DeleteRowsInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `filter` | yes | `Filter` | Rows to delete; an empty filter is refused and removes nothing |
| `filter.filters` | yes | `array or null` | — |
| `filter.type` | yes | `string` | — |
| `ifUpdatedAt` | no | `string` | A row's updatedAt exactly as a previous read returned it; when present the filter must match exactly one row and the delete lands only if that row is unchanged |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `DeleteRowsOutputBody` |
| `default` | Error | `application/problem+json` |

Fields of the `DeleteRowsOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `deleted` | yes | `integer` | — |
| `rows` | yes | `array or null` | — |

Request (minimal — required fields only):

```json
{
  "filter": {
    "filters": [
      {
        "columnName": "string",
        "condition": "string",
        "value": null
      }
    ],
    "type": "string"
  }
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "deleted": 0,
  "rows": [
    {}
  ]
}
```

```sh
curl -X DELETE "$KILASFLOW_HOST/api/v1/datastores/{id}/rows" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"filter":{"filters":[{"columnName":"string","condition":"string","value":null}],"type":"string"}}'
```

Embed: Allow with `datastore:write` — contents of the session’s own datastore: insert, upsert, increment, filtered update and delete, and the CSV import.

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

Fields of the `ClearedDatastoreOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `deleted` | yes | `integer` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "deleted": 0
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/datastores/{id}/clear" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — clearing every row and editing columns reshape the table itself, not its contents.

## Import rows from CSV (`import-datastore-rows`)

`POST /api/v1/datastores/{id}/rows/import`

Validates every record before writing any row: a file with a failed row imports nothing and reports each failure with its line number.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Datastore identifier |

Request body: `text/csv` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `CSVImportReport` |
| `default` | Error | `application/problem+json` |

Fields of the `CSVImportReport` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `failed` | yes | `array or null` | — |
| `inserted` | yes | `integer` | — |
| `skipped` | yes | `integer` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "failed": [
    {
      "column": "string",
      "line": 0,
      "reason": "string",
      "severity": "string"
    }
  ],
  "inserted": 0,
  "skipped": 0
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/datastores/{id}/rows/import" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `datastore:write` — contents of the session’s own datastore: insert, upsert, increment, filtered update and delete, and the CSV import.

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

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/datastores/{id}/rows/export" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `datastore:read` — rows of the session’s own datastore; the CSV export included.
