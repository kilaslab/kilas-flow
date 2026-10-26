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

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/credential-types" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Test an unsaved credential (`test-credential-payload`)

`POST /api/v1/credential-types/{type}/test`

Runs a credential type's probe against a payload that has not been saved. Send credentialId alongside the redaction placeholder to test an edit against stored secrets. A placeholder is filled only while host, port, baseUrl and url match the stored values, and a test that uses a stored secret runs under the stored allowedDomains narrowed by the ones sent. A tenant runs at most four tests at once; one more is answered 429.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `type` | path | yes | string | Credential type ID |

Request body: `application/json` — `TestPayloadBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `allowedDomains` | no | `array or null` | Hosts the probe may reach. Empty means the type's default scope, or any host for a type that has none, unless a stored secret was used: the stored credential's effective scope then applies, narrowed by these. |
| `credentialId` | no | `string` | Stored credential the redaction placeholder resolves against. It must name a credential of this type in the caller's tenant. |
| `fields` | yes | `object` | Field values to test. Send the redaction placeholder to use a stored secret. |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `TestCredentialResource` |
| `default` | Error | `application/problem+json` |

Fields of the `TestCredentialResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `detail` | no | `string` | — |
| `ok` | yes | `boolean` | — |
| `resolvedFromStorage` | no | `array or null` | — |
| `untestable` | no | `boolean` | — |

Request (minimal — required fields only):

```json
{
  "fields": {}
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "detail": "string",
  "ok": false,
  "resolvedFromStorage": [
    "string"
  ],
  "untestable": false
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/credential-types/{type}/test" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"fields":{}}'
```

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

:::code

```sh title="curl"
curl -X GET "$KILASFLOW_HOST/api/v1/credentials" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const credentials = await client.listCredentials(); // names only; values never leave the server
```

```go title="Go"
resp, err := http.Get(baseURL + "/api/v1/credentials")
```

:::

Embed: Allow with `workflow:read` — names are needed to render a credential picker; values are never returned.

## Create a credential (`create-credential`)

`POST /api/v1/credentials`

Stores an encrypted credential payload.

Request body: `application/json` — `CredentialBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `allowedDomains` | no | `array or null` | Hosts this credential may be sent to. An empty list means the type's default scope, or any host for a type that has none. On update, leaving this out keeps the stored scope and an explicit empty list resets it to that default. |
| `fields` | yes | `object` | Field values for the credential type. On create, every value is stored as sent and the redaction placeholder is refused. On update, a field left out, or sent as the redaction placeholder, keeps its stored value; send an empty string to clear one. |
| `name` | yes | `string` | Display name |
| `type` | no | `string` | Credential type ID; immutable after creation |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `CredentialResource` |
| `default` | Error | `application/problem+json` |

Fields of the `CredentialResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `allowedDomains` | yes | `array or null` | Hosts this credential may be sent to, including its type's default when none were saved. Empty means any host. |
| `createdAt` | yes | `string` | — |
| `fields` | yes | `object` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |
| `type` | yes | `string` | — |
| `updatedAt` | yes | `string` | — |
| `usedBy` | no | `array or null` | Workflows whose draft or published version attaches this credential. Returned by a single read only, and left out when nothing uses it or the caller's API key is scoped. |

Request (minimal — required fields only):

```json
{
  "fields": {},
  "name": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "allowedDomains": [
    "string"
  ],
  "createdAt": "2026-01-02T15:04:05Z",
  "fields": {},
  "id": "string",
  "name": "string",
  "type": "string",
  "updatedAt": "2026-01-02T15:04:05Z",
  "usedBy": [
    {
      "active": false,
      "name": "string",
      "nodes": [
        "string"
      ],
      "workflowId": "string"
    }
  ]
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/credentials" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"fields":{},"name":"string"}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const credential = await client.createCredential({
  name: "Acme production", type: "httpHeaderAuth",
  fields: { name: "X-Api-Key", value: apiKey },
});
```

```go title="Go"
payload, _ := json.Marshal(map[string]any{"name": "Acme production", "type": "httpHeaderAuth", "fields": map[string]string{"name": "X-Api-Key", "value": apiKey}})
resp, err := http.Post(baseURL+"/api/v1/credentials", "application/json", bytes.NewReader(payload))
```

:::

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

Fields of the `CredentialResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `allowedDomains` | yes | `array or null` | Hosts this credential may be sent to, including its type's default when none were saved. Empty means any host. |
| `createdAt` | yes | `string` | — |
| `fields` | yes | `object` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |
| `type` | yes | `string` | — |
| `updatedAt` | yes | `string` | — |
| `usedBy` | no | `array or null` | Workflows whose draft or published version attaches this credential. Returned by a single read only, and left out when nothing uses it or the caller's API key is scoped. |

Response (skeleton — every field, placeholder values):

```json
{
  "allowedDomains": [
    "string"
  ],
  "createdAt": "2026-01-02T15:04:05Z",
  "fields": {},
  "id": "string",
  "name": "string",
  "type": "string",
  "updatedAt": "2026-01-02T15:04:05Z",
  "usedBy": [
    {
      "active": false,
      "name": "string",
      "nodes": [
        "string"
      ],
      "workflowId": "string"
    }
  ]
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/credentials/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Update a credential (`update-credential`)

`PUT /api/v1/credentials/{id}`

Replaces the name, and the scope and fields the request sends. A field or scope the request leaves out keeps its stored value.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Credential identifier |

Request body: `application/json` — `CredentialBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `allowedDomains` | no | `array or null` | Hosts this credential may be sent to. An empty list means the type's default scope, or any host for a type that has none. On update, leaving this out keeps the stored scope and an explicit empty list resets it to that default. |
| `fields` | yes | `object` | Field values for the credential type. On create, every value is stored as sent and the redaction placeholder is refused. On update, a field left out, or sent as the redaction placeholder, keeps its stored value; send an empty string to clear one. |
| `name` | yes | `string` | Display name |
| `type` | no | `string` | Credential type ID; immutable after creation |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `CredentialResource` |
| `default` | Error | `application/problem+json` |

Fields of the `CredentialResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `allowedDomains` | yes | `array or null` | Hosts this credential may be sent to, including its type's default when none were saved. Empty means any host. |
| `createdAt` | yes | `string` | — |
| `fields` | yes | `object` | — |
| `id` | yes | `string` | — |
| `name` | yes | `string` | — |
| `type` | yes | `string` | — |
| `updatedAt` | yes | `string` | — |
| `usedBy` | no | `array or null` | Workflows whose draft or published version attaches this credential. Returned by a single read only, and left out when nothing uses it or the caller's API key is scoped. |

Request (minimal — required fields only):

```json
{
  "fields": {},
  "name": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "allowedDomains": [
    "string"
  ],
  "createdAt": "2026-01-02T15:04:05Z",
  "fields": {},
  "id": "string",
  "name": "string",
  "type": "string",
  "updatedAt": "2026-01-02T15:04:05Z",
  "usedBy": [
    {
      "active": false,
      "name": "string",
      "nodes": [
        "string"
      ],
      "workflowId": "string"
    }
  ]
}
```

```sh
curl -X PUT "$KILASFLOW_HOST/api/v1/credentials/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"fields":{},"name":"string"}'
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Delete a credential (`delete-credential`)

`DELETE /api/v1/credentials/{id}`

Permanently removes a stored credential. A credential that a workflow's draft or published version attaches is not deleted: the request is refused with 409, one error per workflow, unless force=true is sent.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Credential identifier |
| `force` | query | no | boolean | Delete the credential even though workflows attach it. Without it, deleting a credential in use is refused with 409 and the workflows named. |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `204` | No Content |  |
| `default` | Error | `application/problem+json` |

```sh
curl -X DELETE "$KILASFLOW_HOST/api/v1/credentials/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

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

Fields of the `TestCredentialResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `detail` | no | `string` | — |
| `ok` | yes | `boolean` | — |
| `resolvedFromStorage` | no | `array or null` | — |
| `untestable` | no | `boolean` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "detail": "string",
  "ok": false,
  "resolvedFromStorage": [
    "string"
  ],
  "untestable": false
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/credentials/{id}/test" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Start Google OAuth (`start-credential-oauth`)

`POST /api/v1/credentials/{id}/oauth/start`

Returns the Google authorization URL for this credential. Open it in a popup (window.open), not an iframe: Google blocks OAuth inside frames. The response also sets an HttpOnly cookie that binds the sign-in to this browser: the callback completes only in the browser that made this request, and only once. The authorization request uses PKCE (S256).

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Credential identifier |
| `Origin` | header | no | string |  |
| `Referer` | header | no | string |  |
| `Host` | header | no | string |  |
| `X-Forwarded-Proto` | header | no | string |  |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `OauthStartResource` |
| `default` | Error | `application/problem+json` |

Fields of the `OauthStartResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `authorizeUrl` | yes | `string` | Google authorization URL. Open it with window.open, never in an iframe. |

Response (skeleton — every field, placeholder values):

```json
{
  "authorizeUrl": "https://flows.example"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/credentials/{id}/oauth/start" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
