---
title: Tenants and accounts
description: "Operator surface: tenants, their users, and keys minted for another tenant. Generated from the live OpenAPI document."
sidebar:
  order: 9
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Operator surface: tenants, their users, and keys minted for another tenant.

## List tenants (`list-tenants`)

`GET /api/v1/tenants`

Every tenant in this deployment with its account count, oldest first. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ListTenantsOutputBody` |
| `default` | Error | `application/problem+json` |

Fields of the `ListTenantsOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `items` | yes | `array or null` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "items": [
    {
      "createdAt": "2026-01-02T15:04:05Z",
      "id": "string",
      "name": "string",
      "userCount": 0
    }
  ]
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/tenants" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Create a tenant (`create-tenant`)

`POST /api/v1/tenants`

Adds a customer. Answers 409 if the ID is taken, so a mistyped create is never mistaken for a successful one. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Request body: `application/json` — `CreateTenantInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `id` | yes | `string` | Stable ID; it appears in URLs and on every row scoped to this tenant |
| `name` | yes | `string` | Display name. Defaults to the ID |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `TenantResource` |
| `default` | Error | `application/problem+json` |

Fields of the `TenantResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |
| `userCount` | yes | `integer` | Accounts this tenant holds |

Request (minimal — required fields only):

```json
{
  "id": "string",
  "name": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "name": "string",
  "userCount": 0
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/tenants" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"id":"string","name":"string"}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const tenant = await client.createTenant({ id: "acme", name: "Acme" }); // operator credential
```

```go title="Go"
payload, _ := json.Marshal(map[string]string{"id": "acme", "name": "Acme"})
resp, err := http.Post(baseURL+"/api/v1/tenants", "application/json", bytes.NewReader(payload))
```

:::

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Read one tenant (`get-tenant`)

`GET /api/v1/tenants/{id}`

The resource the create response points at. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Tenant ID |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `TenantResource` |
| `default` | Error | `application/problem+json` |

Fields of the `TenantResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |
| `userCount` | yes | `integer` | Accounts this tenant holds |

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "name": "string",
  "userCount": 0
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/tenants/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Delete a tenant and everything it owns (`delete-tenant`)

`DELETE /api/v1/tenants/{id}`

Deletes a customer: its executions and their payload files, workflows and their versions, credentials and secret bindings, schedules, webhook deliveries and routes, datastores and their physical tables, vector rows, accounts, keys and finally the tenant row itself. This is irreversible, and it is reserved for the operator credential: a customer's key is refused like any other non-operator principal. The call is idempotent — repeat it until every count is zero and tenantRemoved is false, which is also how a deletion that a client timeout interrupted is resumed. An unknown id answers 200 with zero counts rather than 404: a deletion means "remove everything keyed to this id", and that has to clean up rows an earlier partial deletion or a stale embed session left behind. The operator's own tenant is refused with 409, because deleting it deletes the credential the caller is using. The tenant's keys and accounts are locked out first, so nothing can write while its rows are going, and a deletion that fails leaves it locked out — the safe direction. A failure names the step it stopped in and the same request resumes from there; the deletion also runs on past the client's own timeout, so a caller that gives up early should send the request again rather than assume it stopped. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Tenant ID |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `TenantDeletionResource` |
| `default` | Error | `application/problem+json` |

Fields of the `TenantDeletionResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `binaries` | yes | `BinaryRemoval` | Payload files and bytes removed from disk |
| `binaries.bytes` | yes | `integer` | Total size of the files removed |
| `binaries.executions` | yes | `integer` | Payload directories removed, one per execution |
| `binaries.files` | yes | `integer` | Files removed below those directories |
| `datastoreTables` | yes | `integer` | Physical datastore tables dropped, which are not rows in a covered table |
| `removed` | yes | `object` | Rows removed per table; every covered table appears, including those found empty |
| `tenantId` | yes | `string` | The tenant this deletion removed |
| `tenantRemoved` | yes | `boolean` | Whether the tenant's own row was deleted by this call |

Response (skeleton — every field, placeholder values):

```json
{
  "binaries": {
    "bytes": 0,
    "executions": 0,
    "files": 0
  },
  "datastoreTables": 0,
  "removed": {},
  "tenantId": "string",
  "tenantRemoved": false
}
```

```sh
curl -X DELETE "$KILASFLOW_HOST/api/v1/tenants/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## List a tenant's accounts (`list-tenant-users`)

`GET /api/v1/tenants/{id}/users`

Reads one tenant's accounts. No password hash is ever included. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Tenant ID |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ListTenantUsersOutputBody` |
| `default` | Error | `application/problem+json` |

Fields of the `ListTenantUsersOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `items` | yes | `array or null` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "items": [
    {
      "createdAt": "2026-01-02T15:04:05Z",
      "disabledAt": "2026-01-02T15:04:05Z",
      "email": "user@example.com",
      "id": "string",
      "name": "string",
      "tenantId": "string"
    }
  ]
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/tenants/{id}/users" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Create an account in a tenant (`create-tenant-user`)

`POST /api/v1/tenants/{id}/users`

Creates a dashboard account that can sign in immediately. This is the path that used to be reachable only through the one-time bootstrap or a raw insert. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Tenant the account belongs to |

Request body: `application/json` — `CreateTenantUserInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `email` | yes | `string` | Sign-in address. Unique across the deployment, because login presents an address and nothing else |
| `name` | yes | `string` | Display name |
| `password` | yes | `string` | Hashed with PBKDF2 before it reaches the database |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `UserResource` |
| `default` | Error | `application/problem+json` |

Fields of the `UserResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `disabledAt` | no | `string` | — |
| `email` | yes | `string` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |
| `tenantId` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "email": "user@example.com",
  "name": "string",
  "password": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "disabledAt": "2026-01-02T15:04:05Z",
  "email": "user@example.com",
  "id": "string",
  "name": "string",
  "tenantId": "string"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/tenants/{id}/users" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"email":"user@example.com","name":"string","password":"string"}'
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Disable an account (`disable-tenant-user`)

`POST /api/v1/tenants/{id}/users/{userId}/disable`

Stops the account signing in from the next request onwards. The row is kept so the workflows and executions it authored still have a name. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Tenant the account belongs to |
| `userId` | path | yes | string | Account to disable or re-enable |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `UserResource` |
| `default` | Error | `application/problem+json` |

Fields of the `UserResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `disabledAt` | no | `string` | — |
| `email` | yes | `string` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |
| `tenantId` | yes | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "disabledAt": "2026-01-02T15:04:05Z",
  "email": "user@example.com",
  "id": "string",
  "name": "string",
  "tenantId": "string"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/tenants/{id}/users/{userId}/disable" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Re-enable an account (`enable-tenant-user`)

`POST /api/v1/tenants/{id}/users/{userId}/enable`

Clears the offboarding marker, so the account signs in again with its existing password. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Tenant the account belongs to |
| `userId` | path | yes | string | Account to disable or re-enable |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `UserResource` |
| `default` | Error | `application/problem+json` |

Fields of the `UserResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `disabledAt` | no | `string` | — |
| `email` | yes | `string` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |
| `tenantId` | yes | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "disabledAt": "2026-01-02T15:04:05Z",
  "email": "user@example.com",
  "id": "string",
  "name": "string",
  "tenantId": "string"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/tenants/{id}/users/{userId}/enable" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Replace an account's password (`set-tenant-user-password`)

`POST /api/v1/tenants/{id}/users/{userId}/password`

Sets a new password. This is the operator's reset: the old password stops working at once, and sessions minted under it are cut loose by the account's password version. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string |  |
| `userId` | path | yes | string |  |

Request body: `application/json` — `SetUserPasswordInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `password` | yes | `string` | Replacement password. The old one stops working immediately |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `UserResource` |
| `default` | Error | `application/problem+json` |

Fields of the `UserResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `disabledAt` | no | `string` | — |
| `email` | yes | `string` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |
| `tenantId` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "password": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "disabledAt": "2026-01-02T15:04:05Z",
  "email": "user@example.com",
  "id": "string",
  "name": "string",
  "tenantId": "string"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/tenants/{id}/users/{userId}/password" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"password":"string"}'
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Mint an API key for a tenant (`create-tenant-api-key`)

`POST /api/v1/tenants/{id}/api-keys`

Mints a key on another tenant's behalf. The existing /api-keys endpoint can only mint for the caller, so without this a new tenant could be created and then never used. The token is returned exactly once and is never listed. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Tenant the key will belong to |

Request body: `application/json` — `CreateTenantAPIKeyInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `label` | yes | `string` | How the key will be recognised later |

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

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/tenants/{id}/api-keys" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"label":"string"}'
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
