---
title: Authentication and keys
description: Sessions, API keys, and stream tickets. Generated from the live OpenAPI document.
sidebar:
  order: 4
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Sessions, API keys, and stream tickets.

## Sign in (`login`)

`POST /api/v1/auth/login`

Exchanges an email and password for a session cookie. The cookie is HttpOnly, so the browser can never read it back.

Request body: `application/json` — `LoginInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `PrincipalResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Sign out (`logout`)

`POST /api/v1/auth/logout`

Clears the session cookie in this browser. The session token itself is stateless and stays valid until it expires, so a copy taken beforehand is not revoked.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `204` | No Content |  |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Describe the current caller (`get-me`)

`GET /api/v1/auth/me`

Reports the tenant and identity this request authenticated as.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `PrincipalResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## List API keys (`list-api-keys`)

`GET /api/v1/api-keys`

Lists this tenant's keys. No secret is ever included.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ListAPIKeysOutputBody` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Create an API key (`create-api-key`)

`POST /api/v1/api-keys`

Mints a key scoped to the calling tenant and returns it in full exactly once. The server keeps only a hash and cannot show it again.

Request body: `application/json` — `CreateAPIKeyInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `CreatedAPIKeyResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Revoke an API key (`revoke-api-key`)

`DELETE /api/v1/api-keys/{id}`

Stops a key authenticating, immediately and permanently. The row is kept so an audit still has something to name.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string |  |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `APIKeyResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Mint an execution stream ticket (`create-stream-ticket`)

`POST /api/v1/stream-tickets`

EventSource cannot send an Authorization header, so a caller exchanges its credential for a single-use ticket and spends it on the events endpoint. Tickets live for seconds and name one execution.

Request body: `application/json` — `CreateStreamTicketInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `StreamTicketResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
