---
title: Embed
description: "Mint a session that confines an embedded editor to one workflow. Generated from the live OpenAPI document."
sidebar:
  order: 11
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Mint a session that confines an embedded editor to one workflow.

## Create an embed session (`create-embed-session`)

`POST /api/v1/embed-sessions`

Mints a short-lived token for one host origin, scoped to one workflow or one datastore. The host passes it to the iframe over postMessage.

Request body: `application/json` — `EmbedSessionBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `branding` | no | `Branding` | Validated white-label values; never markup |
| `branding.accent` | no | `string` | — |
| `branding.hideRun` | no | `boolean` | — |
| `branding.hideSave` | no | `boolean` | — |
| `branding.logoUrl` | no | `string` | — |
| `branding.name` | no | `string` | — |
| `datastoreId` | no | `string` | Datastore this session may touch; exactly one of this and workflowId |
| `origin` | yes | `string` | Exact origin of the page that will frame the editor |
| `scopes` | yes | `array or null` | workflow:read, workflow:write, workflow:run, workflow:publish, workflow:activate, datastore:read, datastore:write. workflow:publish pins a revision as the version production traffic runs and workflow:activate turns the workflow on and off; neither is implied by write. |
| `ttlSeconds` | no | `integer` | Session lifetime; capped by the server |
| `workflowId` | no | `string` | Workflow this session may open; exactly one of this and datastoreId |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `EmbedSessionResource` |
| `default` | Error | `application/problem+json` |

Fields of the `EmbedSessionResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `branding` | no | `Branding` | — |
| `branding.accent` | no | `string` | — |
| `branding.hideRun` | no | `boolean` | — |
| `branding.hideSave` | no | `boolean` | — |
| `branding.logoUrl` | no | `string` | — |
| `branding.name` | no | `string` | — |
| `datastoreId` | no | `string` | — |
| `embedUrl` | yes | `string` | — |
| `expiresAt` | yes | `string` | — |
| `origin` | yes | `string` | — |
| `scopes` | yes | `array or null` | — |
| `token` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "origin": "string",
  "scopes": [
    "string"
  ]
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "branding": {
    "accent": "string",
    "hideRun": false,
    "hideSave": false,
    "logoUrl": "https://flows.example",
    "name": "string"
  },
  "datastoreId": "string",
  "embedUrl": "https://flows.example",
  "expiresAt": "2026-01-02T15:04:05Z",
  "origin": "string",
  "scopes": [
    "string"
  ],
  "token": "string"
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/embed-sessions" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"origin":"string","scopes":["string"]}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const session = await client.createEmbedSession({
  workflowId,
  scopes: ["workflow:read", "workflow:write"],
  origin: "https://app.example.com",
});
```

```go title="Go"
payload, _ := json.Marshal(map[string]any{"workflowId": workflowId, "scopes": []string{"workflow:read", "workflow:write"}, "origin": "https://app.example.com"})
resp, err := http.Post(baseURL+"/api/v1/embed-sessions", "application/json", bytes.NewReader(payload))
```

:::

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
