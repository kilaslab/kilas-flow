---
title: Schedules
description: "Cron-style triggers owned by a workflow. Generated from the live OpenAPI document."
sidebar:
  order: 5
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Cron-style triggers owned by a workflow.

## List schedules (`list-schedules`)

`GET /api/v1/schedules`

Returns one page of cron schedules, oldest first. The next page's cursor is in the X-Next-Cursor response header, empty on the last page.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `limit` | query | no | integer | Maximum schedules to return (default 100) |
| `cursor` | query | no | string | Opaque cursor from the previous page's X-Next-Cursor header |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `array or null` |
| `default` | Error | `application/problem+json` |

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/schedules" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Create a schedule (`create-schedule`)

`POST /api/v1/schedules`

Binds a cron expression to a workflow.

Request body: `application/json` — `ScheduleBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | Whether this schedule fires |
| `cron` | yes | `string` | Standard five-field cron expression, evaluated in UTC |
| `nodeId` | no | `string` | Schedule trigger node in that workflow |
| `workflowId` | no | `string` | Workflow to run; immutable after creation |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `ScheduleResource` |
| `default` | Error | `application/problem+json` |

Fields of the `ScheduleResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | — |
| `createdAt` | yes | `string` | — |
| `cron` | yes | `string` | — |
| `id` | yes | `string` | — |
| `lastRunAt` | no | `string` | — |
| `nextRunAt` | no | `string` | — |
| `nodeId` | no | `string` | — |
| `updatedAt` | yes | `string` | — |
| `workflowId` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "active": false,
  "cron": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "active": false,
  "createdAt": "2026-01-02T15:04:05Z",
  "cron": "string",
  "id": "string",
  "lastRunAt": "2026-01-02T15:04:05Z",
  "nextRunAt": "2026-01-02T15:04:05Z",
  "nodeId": "string",
  "updatedAt": "2026-01-02T15:04:05Z",
  "workflowId": "string"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/schedules" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"active":false,"cron":"string"}'
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Update a schedule (`update-schedule`)

`PUT /api/v1/schedules/{id}`

Replaces the cron expression and activation state.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Schedule identifier |

Request body: `application/json` — `ScheduleBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | Whether this schedule fires |
| `cron` | yes | `string` | Standard five-field cron expression, evaluated in UTC |
| `nodeId` | no | `string` | Schedule trigger node in that workflow |
| `workflowId` | no | `string` | Workflow to run; immutable after creation |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ScheduleResource` |
| `default` | Error | `application/problem+json` |

Fields of the `ScheduleResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | — |
| `createdAt` | yes | `string` | — |
| `cron` | yes | `string` | — |
| `id` | yes | `string` | — |
| `lastRunAt` | no | `string` | — |
| `nextRunAt` | no | `string` | — |
| `nodeId` | no | `string` | — |
| `updatedAt` | yes | `string` | — |
| `workflowId` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "active": false,
  "cron": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "active": false,
  "createdAt": "2026-01-02T15:04:05Z",
  "cron": "string",
  "id": "string",
  "lastRunAt": "2026-01-02T15:04:05Z",
  "nextRunAt": "2026-01-02T15:04:05Z",
  "nodeId": "string",
  "updatedAt": "2026-01-02T15:04:05Z",
  "workflowId": "string"
}
```

```sh
curl -X PUT "$KILASFLOW_HOST/api/v1/schedules/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"active":false,"cron":"string"}'
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Delete a schedule (`delete-schedule`)

`DELETE /api/v1/schedules/{id}`

Removes a cron schedule.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Schedule identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `204` | No Content |  |
| `default` | Error | `application/problem+json` |

```sh
curl -X DELETE "$KILASFLOW_HOST/api/v1/schedules/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
