---
title: Credentials
description: "Credential types and stored credentials. Reads never return values. Generated from the live OpenAPI document."
sidebar:
  order: 3
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Credential types and stored credentials. Reads never return values.

## List credential types (`list-credential-types`)

`GET /api/v1/credential-types`

Returns the server-defined credential types and their editor field metadata.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `array or null` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Test an unsaved credential (`test-credential-payload`)

`POST /api/v1/credential-types/{type}/test`

Runs a credential type's probe against a payload that has not been saved. Send credentialId alongside the redaction placeholder to test an edit against stored secrets.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `type` | path | yes | string | Credential type ID |

Request body: `application/json` — `TestPayloadBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `TestCredentialResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## List credentials (`list-credentials`)

`GET /api/v1/credentials`

Returns stored credentials without any secret value.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `limit` | query | no | integer | Maximum credentials to return (default 100) |
| `cursor` | query | no | string | Opaque cursor from a previous listing's X-Next-Cursor header |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `array or null` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:read` — names are needed to render a credential picker; values are never returned.

## Create a credential (`create-credential`)

`POST /api/v1/credentials`

Stores an encrypted credential payload.

Request body: `application/json` — `CredentialBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `CredentialResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Get a credential (`get-credential`)

`GET /api/v1/credentials/{id}`

Returns one credential without any secret value.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Credential identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `CredentialResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Update a credential (`update-credential`)

`PUT /api/v1/credentials/{id}`

Replaces name, scope, and any field sent with a new value.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Credential identifier |

Request body: `application/json` — `CredentialBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `CredentialResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Delete a credential (`delete-credential`)

`DELETE /api/v1/credentials/{id}`

Permanently removes a stored credential.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Credential identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `204` | No Content |  |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Test a credential (`test-credential`)

`POST /api/v1/credentials/{id}/test`

Runs the credential type's declared probe and reports pass or fail. No secret and no remote response body is returned.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string |  |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `TestCredentialResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
