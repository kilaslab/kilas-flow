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

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Create a schedule (`create-schedule`)

`POST /api/v1/schedules`

Binds a cron expression to a workflow.

Request body: `application/json` — `ScheduleBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `ScheduleResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Update a schedule (`update-schedule`)

`PUT /api/v1/schedules/{id}`

Replaces the cron expression and activation state.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Schedule identifier |

Request body: `application/json` — `ScheduleBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ScheduleResource` |
| `default` | Error | `application/problem+json` |

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

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
