---
title: Variables
description: "The tenant’s key/value pairs every workflow reads as `$vars.KEY`. Generated from the live OpenAPI document."
sidebar:
  order: 6
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

The tenant’s key/value pairs every workflow reads as `$vars.KEY`.

## List variables (`list-variables`)

`GET /api/v1/variables`

Returns every variable of the tenant, ordered by key. Every workflow of the tenant reads them as `$vars.KEY`. The list is not paged: a tenant holds at most 500 variables.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `array or null` |
| `default` | Error | `application/problem+json` |

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/variables" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — variables are tenant-wide: every workflow of the tenant reads them, so a session confined to one workflow may neither list nor change them.

## Create a variable (`create-variable`)

`POST /api/v1/variables`

Adds a variable. A key the tenant already has answers 409. The next execution that starts reads it.

Request body: `application/json` — `VariableBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `key` | yes | `string` | Letters, digits and underscores, starting with a letter or underscore, so it reads as $vars.KEY. |
| `value` | yes | `string` | Plain text, at most 4096 bytes. Stored unencrypted: a secret belongs in a credential. |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `VariableResource` |
| `default` | Error | `application/problem+json` |

Fields of the `VariableResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `key` | yes | `string` | The name a workflow reads, as $vars.KEY. |
| `updatedAt` | yes | `string` | — |
| `value` | yes | `string` | Plain text, stored unencrypted. Never put a secret here: use a credential. |

Request (minimal — required fields only):

```json
{
  "key": "string",
  "value": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "key": "string",
  "updatedAt": "2026-01-02T15:04:05Z",
  "value": "string"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/variables" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"key":"string","value":"string"}'
```

Embed: Deny — variables are tenant-wide: every workflow of the tenant reads them, so a session confined to one workflow may neither list nor change them.

## Update a variable (`update-variable`)

`PUT /api/v1/variables/{id}`

Replaces a variable's key and value. Renaming onto a key the tenant already has answers 409. Executions already running keep the value they started with.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Variable identifier |

Request body: `application/json` — `VariableBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `key` | yes | `string` | Letters, digits and underscores, starting with a letter or underscore, so it reads as $vars.KEY. |
| `value` | yes | `string` | Plain text, at most 4096 bytes. Stored unencrypted: a secret belongs in a credential. |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `VariableResource` |
| `default` | Error | `application/problem+json` |

Fields of the `VariableResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `key` | yes | `string` | The name a workflow reads, as $vars.KEY. |
| `updatedAt` | yes | `string` | — |
| `value` | yes | `string` | Plain text, stored unencrypted. Never put a secret here: use a credential. |

Request (minimal — required fields only):

```json
{
  "key": "string",
  "value": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "key": "string",
  "updatedAt": "2026-01-02T15:04:05Z",
  "value": "string"
}
```

```sh
curl -X PUT "$KILASFLOW_HOST/api/v1/variables/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"key":"string","value":"string"}'
```

Embed: Deny — variables are tenant-wide: every workflow of the tenant reads them, so a session confined to one workflow may neither list nor change them.

## Delete a variable (`delete-variable`)

`DELETE /api/v1/variables/{id}`

Removes a variable. A workflow that still reads it gets an empty value from the next execution on.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Variable identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `204` | No Content |  |
| `default` | Error | `application/problem+json` |

```sh
curl -X DELETE "$KILASFLOW_HOST/api/v1/variables/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — variables are tenant-wide: every workflow of the tenant reads them, so a session confined to one workflow may neither list nor change them.
