---
title: Tenants and accounts
description: "Operator surface: tenants, their users, and keys minted for another tenant. Generated from the live OpenAPI document."
sidebar:
  order: 8
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

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Create a tenant (`create-tenant`)

`POST /api/v1/tenants`

Adds a customer. Answers 409 if the ID is taken, so a mistyped create is never mistaken for a successful one. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Request body: `application/json` — `CreateTenantInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `TenantResource` |
| `default` | Error | `application/problem+json` |

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

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Create an account in a tenant (`create-tenant-user`)

`POST /api/v1/tenants/{id}/users`

Creates a dashboard account that can sign in immediately. This is the path that used to be reachable only through the one-time bootstrap or a raw insert. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Tenant the account belongs to |

Request body: `application/json` — `CreateTenantUserInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `UserResource` |
| `default` | Error | `application/problem+json` |

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

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `UserResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Mint an API key for a tenant (`create-tenant-api-key`)

`POST /api/v1/tenants/{id}/api-keys`

Mints a key on another tenant's behalf. The existing /api-keys endpoint can only mint for the caller, so without this a new tenant could be created and then never used. The token is returned exactly once and is never listed. Requires the operator credential: an API key scoped to the operator tenant. Any other principal, a customer's key or any session, is refused.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Tenant the key will belong to |

Request body: `application/json` — `CreateTenantAPIKeyInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `CreatedAPIKeyResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
