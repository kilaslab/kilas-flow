---
title: Authentication and keys
description: "Sessions, API keys, and stream tickets. Generated from the live OpenAPI document."
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

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `email` | yes | `string` | Account email address |
| `password` | yes | `string` | Account password |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `PrincipalResource` |
| `default` | Error | `application/problem+json` |

Fields of the `PrincipalResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `email` | no | `string` | — |
| `expiresAt` | no | `string` | Absent when the key does not expire |
| `keyId` | no | `string` | Set for a machine caller |
| `kind` | yes | `string` | api_key or session |
| `label` | no | `string` | The key's name, for a machine caller |
| `name` | no | `string` | — |
| `scopes` | no | `array or null` | Absent on a tenant-wide key; present on an agent token |
| `tenantId` | yes | `string` | Tenant every request from this caller is scoped to |
| `userId` | no | `string` | Set for a signed-in person |
| `workflowId` | no | `string` | Set when a scoped key is bound to one workflow |

Request (minimal — required fields only):

```json
{
  "email": "user@example.com",
  "password": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "email": "user@example.com",
  "expiresAt": "2026-01-02T15:04:05Z",
  "keyId": "string",
  "kind": "string",
  "label": "string",
  "name": "string",
  "scopes": [
    "string"
  ],
  "tenantId": "string",
  "userId": "string",
  "workflowId": "string"
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/auth/login" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"email":"user@example.com","password":"string"}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const principal = await client.login({ email, password }); // session arrives as an HttpOnly cookie
```

```go title="Go"
payload, _ := json.Marshal(map[string]string{"email": email, "password": password})
resp, err := http.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(payload))
```

:::

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Sign out (`logout`)

`POST /api/v1/auth/logout`

Clears the session cookie in this browser. The session token itself is stateless and stays valid until it expires, so a copy taken beforehand is not revoked.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `204` | No Content |  |
| `default` | Error | `application/problem+json` |

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/auth/logout" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Describe the current caller (`get-me`)

`GET /api/v1/auth/me`

Reports the tenant and identity this request authenticated as.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `PrincipalResource` |
| `default` | Error | `application/problem+json` |

Fields of the `PrincipalResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `email` | no | `string` | — |
| `expiresAt` | no | `string` | Absent when the key does not expire |
| `keyId` | no | `string` | Set for a machine caller |
| `kind` | yes | `string` | api_key or session |
| `label` | no | `string` | The key's name, for a machine caller |
| `name` | no | `string` | — |
| `scopes` | no | `array or null` | Absent on a tenant-wide key; present on an agent token |
| `tenantId` | yes | `string` | Tenant every request from this caller is scoped to |
| `userId` | no | `string` | Set for a signed-in person |
| `workflowId` | no | `string` | Set when a scoped key is bound to one workflow |

Response (skeleton — every field, placeholder values):

```json
{
  "email": "user@example.com",
  "expiresAt": "2026-01-02T15:04:05Z",
  "keyId": "string",
  "kind": "string",
  "label": "string",
  "name": "string",
  "scopes": [
    "string"
  ],
  "tenantId": "string",
  "userId": "string",
  "workflowId": "string"
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/auth/me" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## List API keys (`list-api-keys`)

`GET /api/v1/api-keys`

Lists one page of this tenant's keys, newest first. No secret is ever included. The next page's cursor is in the X-Next-Cursor response header, empty on the last page.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `limit` | query | no | integer | Maximum keys to return (default 100) |
| `cursor` | query | no | string | Opaque cursor from the previous page's X-Next-Cursor header |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ListAPIKeysOutputBody` |
| `default` | Error | `application/problem+json` |

Fields of the `ListAPIKeysOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `items` | yes | `array or null` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "items": [
    {
      "createdAt": "2026-01-02T15:04:05Z",
      "expiresAt": "2026-01-02T15:04:05Z",
      "id": "string",
      "label": "string",
      "lastUsedAt": "2026-01-02T15:04:05Z",
      "prefix": "string",
      "revokedAt": "2026-01-02T15:04:05Z",
      "scopes": [
        "string"
      ],
      "workflowId": "string"
    }
  ]
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/api-keys" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Create an API key (`create-api-key`)

`POST /api/v1/api-keys`

Mints a key scoped to the calling tenant and returns it in full exactly once. The server keeps only a hash and cannot show it again.

Request body: `application/json` — `CreateAPIKeyInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `expiresAt` | no | `string` | When the key stops working. Omit for no expiry. |
| `label` | yes | `string` | How this key will be recognised later |
| `scopes` | no | `array or null` | workflow:read, workflow:write, workflow:run, datastore:read, datastore:write. Omit for a tenant-wide key. |
| `workflowId` | no | `string` | Narrows a scoped key to one workflow |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `CreatedAPIKeyResource` |
| `default` | Error | `application/problem+json` |

Fields of the `CreatedAPIKeyResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `expiresAt` | no | `string` | — |
| `id` | yes | `string` | — |
| `label` | yes | `string` | — |
| `prefix` | yes | `string` | — |
| `scopes` | no | `array or null` | Absent on a tenant-wide key; present on an agent token |
| `token` | yes | `string` | The full key. Shown once and never again. |
| `workflowId` | no | `string` | — |

Request (minimal — required fields only):

```json
{
  "label": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "expiresAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "label": "string",
  "prefix": "string",
  "scopes": [
    "string"
  ],
  "token": "string",
  "workflowId": "string"
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/api-keys" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"label":"string"}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const key = await client.createApiKey("nightly-sync"); // key.token is shown exactly once
```

```go title="Go"
payload, _ := json.Marshal(map[string]string{"label": "nightly-sync"})
resp, err := http.Post(baseURL+"/api/v1/api-keys", "application/json", bytes.NewReader(payload))
```

:::

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

Fields of the `APIKeyResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `expiresAt` | no | `string` | — |
| `id` | yes | `string` | — |
| `label` | yes | `string` | — |
| `lastUsedAt` | no | `string` | Accurate to about a minute |
| `prefix` | yes | `string` | Public handle, enough to recognise a key in a log |
| `revokedAt` | no | `string` | — |
| `scopes` | no | `array or null` | Absent on a tenant-wide key; present on an agent token |
| `workflowId` | no | `string` | Set when a scoped key is bound to one workflow |

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "expiresAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "label": "string",
  "lastUsedAt": "2026-01-02T15:04:05Z",
  "prefix": "string",
  "revokedAt": "2026-01-02T15:04:05Z",
  "scopes": [
    "string"
  ],
  "workflowId": "string"
}
```

```sh
curl -X DELETE "$KILASFLOW_HOST/api/v1/api-keys/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Mint an execution stream ticket (`create-stream-ticket`)

`POST /api/v1/stream-tickets`

EventSource cannot send an Authorization header, so a caller exchanges its credential for a single-use ticket and spends it on the events endpoint. Tickets live for seconds and name one execution.

Request body: `application/json` — `CreateStreamTicketInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `executionId` | yes | `string` | Execution whose events will be streamed |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `StreamTicketResource` |
| `default` | Error | `application/problem+json` |

Fields of the `StreamTicketResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `executionId` | yes | `string` | — |
| `expiresAt` | yes | `string` | — |
| `ticket` | yes | `string` | Spend as the ticket query parameter on the events endpoint |

Request (minimal — required fields only):

```json
{
  "executionId": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "executionId": "string",
  "expiresAt": "2026-01-02T15:04:05Z",
  "ticket": "string"
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/stream-tickets" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"executionId":"string"}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const { ticket } = await client.createStreamTicket(executionId); // spent as ?ticket= by EventSource
```

```go title="Go"
resp, err := http.Post(baseURL+"/api/v1/executions/"+url.PathEscape(executionId)+"/stream-ticket", "application/json", nil)
```

:::

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
