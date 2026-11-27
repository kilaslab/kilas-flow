---
title: Notifications
description: "The signed outbound notifications a tenant receives, and their delivery log. Generated from the live OpenAPI document."
sidebar:
  order: 7
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

The signed outbound notifications a tenant receives, and their delivery log.

## List notification deliveries (`list-notification-deliveries`)

`GET /api/v1/notifications/deliveries`

Returns the newest outbound notification events of the caller's tenant with their delivery state: attempts made, the last response status and error, and when the next attempt is due. Events are kept for notifications.retention.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `status` | query | no | string | Only events in this state |
| `limit` | query | no | integer | Maximum events to return (default 50) |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `array or null` |
| `default` | Error | `application/problem+json` |

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/notifications/deliveries" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — `403` “This embed session cannot read notification deliveries.”
