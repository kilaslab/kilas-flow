---
title: Workflows
description: "Create, read, update, run, activate, and version workflows. Generated from the live OpenAPI document."
sidebar:
  order: 1
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Create, read, update, run, activate, and version workflows.

## Create a workflow draft (`create-workflow`)

`POST /api/v1/workflows`

Creates revision 1 of a canonical workflow document.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `X-KilasFlow-Skills-Used` | header | no | string | Comma-separated names of the skills an agent used to make this call, recorded on the revision it creates |

Request body: `application/json` — `WorkflowDocumentInput` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `baseVersionId` | no | `string` | Latest version ID the editor saved from; a save from a stale revision is refused with 409 |
| `connections` | yes | `array or null` | — |
| `name` | yes | `string` | — |
| `nodes` | yes | `array or null` | — |
| `pinData` | no | `object` | Node output the author pinned, keyed by node name: a run feeds a node's pinned items as its output instead of executing the node |
| `schemaVersion` | yes | `integer` | — |
| `settings` | yes | `object` | — |
| `tags` | no | `array or null` | Optional labels that group workflows. At most 20; each 1 to 24 characters with no leading or trailing whitespace, no comma and no control character; unique ignoring case. Order and case are kept. The list filters by one with ?tag= |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

Fields of the `WorkflowResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | — |
| `activeVersion` | no | `WorkflowVersionResource` | — |
| `activeVersion.createdAt` | yes | `string` | — |
| `activeVersion.document` | yes | `Document` | — |
| `activeVersion.document.connections` | yes | `array or null` | — |
| `activeVersion.document.id` | yes | `string` | — |
| `activeVersion.document.name` | yes | `string` | — |
| `activeVersion.document.nodes` | yes | `array or null` | — |
| `activeVersion.document.pinData` | no | `object` | — |
| `activeVersion.document.schemaVersion` | yes | `integer` | — |
| `activeVersion.document.settings` | yes | `object` | — |
| `activeVersion.document.tags` | no | `array or null` | — |
| `activeVersion.id` | yes | `string` | — |
| `activeVersion.revision` | yes | `integer` | — |
| `activeVersion.schemaVersion` | yes | `integer` | — |
| `activeVersion.workflowId` | yes | `string` | — |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `latestVersion` | yes | `WorkflowVersionResource` | — |
| `latestVersion.createdAt` | yes | `string` | — |
| `latestVersion.document` | yes | `Document` | — |
| `latestVersion.document.connections` | yes | `array or null` | — |
| `latestVersion.document.id` | yes | `string` | — |
| `latestVersion.document.name` | yes | `string` | — |
| `latestVersion.document.nodes` | yes | `array or null` | — |
| `latestVersion.document.pinData` | no | `object` | — |
| `latestVersion.document.schemaVersion` | yes | `integer` | — |
| `latestVersion.document.settings` | yes | `object` | — |
| `latestVersion.document.tags` | no | `array or null` | — |
| `latestVersion.id` | yes | `string` | — |
| `latestVersion.revision` | yes | `integer` | — |
| `latestVersion.schemaVersion` | yes | `integer` | — |
| `latestVersion.workflowId` | yes | `string` | — |
| `name` | yes | `string` | — |
| `updatedAt` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "connections": [
    {
      "id": "string",
      "kind": "string",
      "source": {
        "nodeId": "string",
        "port": "string"
      },
      "target": {
        "nodeId": "string",
        "port": "string"
      }
    }
  ],
  "name": "string",
  "nodes": [
    {
      "credentials": {},
      "disabled": false,
      "id": "string",
      "name": "string",
      "parameters": {},
      "position": {
        "x": 0,
        "y": 0
      },
      "settings": {},
      "type": "string",
      "typeVersion": 1
    }
  ],
  "schemaVersion": 0,
  "settings": {}
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "active": false,
  "activeVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "latestVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "name": "string",
  "updatedAt": "2026-01-02T15:04:05Z"
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/workflows" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"connections":[{"id":"string","kind":"string","source":{"nodeId":"string","port":"string"},"target":{"nodeId":"string","port":"string"}}],"name":"string","nodes":[{"credentials":{},"disabled":false,"id":"string","name":"string","parameters":{},"position":{"x":0,"y":0},"settings":{},"type":"string","typeVersion":1}],"schemaVersion":0,"settings":{}}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const workflow = await client.createWorkflow(document);
```

```go title="Go"
payload, _ := json.Marshal(document)
resp, err := http.Post(baseURL+"/api/v1/workflows", "application/json", bytes.NewReader(payload))
```

:::

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Validate a workflow document (`validate-workflow-document`)

`POST /api/v1/workflows/validate`

Compiles the supplied document with the same compiler activation uses and answers the diagnostics, saving nothing. The catalogue is narrowed to the caller's tenant, so a document that references a node this workspace may not use says so here rather than at activation.

Request body: `application/json` — `WorkflowDocumentInput` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `baseVersionId` | no | `string` | Latest version ID the editor saved from; a save from a stale revision is refused with 409 |
| `connections` | yes | `array or null` | — |
| `name` | yes | `string` | — |
| `nodes` | yes | `array or null` | — |
| `pinData` | no | `object` | Node output the author pinned, keyed by node name: a run feeds a node's pinned items as its output instead of executing the node |
| `schemaVersion` | yes | `integer` | — |
| `settings` | yes | `object` | — |
| `tags` | no | `array or null` | Optional labels that group workflows. At most 20; each 1 to 24 characters with no leading or trailing whitespace, no comma and no control character; unique ignoring case. Order and case are kept. The list filters by one with ?tag= |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ValidateWorkflowResource` |
| `default` | Error | `application/problem+json` |

Fields of the `ValidateWorkflowResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `diagnostics` | yes | `array or null` | What the compiler refused, stated as the import report states it: severity blocking, the node where one is named, and the reason |
| `valid` | yes | `boolean` | True when the document compiles and would run |
| `warnings` | no | `array or null` | What this server could not check, such as a Go Code node it has no toolchain to compile. A warning never makes the document invalid |

Request (minimal — required fields only):

```json
{
  "connections": [
    {
      "id": "string",
      "kind": "string",
      "source": {
        "nodeId": "string",
        "port": "string"
      },
      "target": {
        "nodeId": "string",
        "port": "string"
      }
    }
  ],
  "name": "string",
  "nodes": [
    {
      "credentials": {},
      "disabled": false,
      "id": "string",
      "name": "string",
      "parameters": {},
      "position": {
        "x": 0,
        "y": 0
      },
      "settings": {},
      "type": "string",
      "typeVersion": 1
    }
  ],
  "schemaVersion": 0,
  "settings": {}
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "diagnostics": [
    {
      "field": "string",
      "nodeId": "string",
      "nodeName": "string",
      "reason": "string",
      "severity": "blocking",
      "type": "string",
      "typeVersion": 1
    }
  ],
  "valid": false,
  "warnings": [
    {
      "field": "string",
      "nodeId": "string",
      "reason": "string"
    }
  ]
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/workflows/validate" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"connections":[{"id":"string","kind":"string","source":{"nodeId":"string","port":"string"},"target":{"nodeId":"string","port":"string"}}],"name":"string","nodes":[{"credentials":{},"disabled":false,"id":"string","name":"string","parameters":{},"position":{"x":0,"y":0},"settings":{},"type":"string","typeVersion":1}],"schemaVersion":0,"settings":{}}'
```

Embed: Allow with `workflow:read` — a dry run that compiles the supplied document and saves nothing.

## List workflows (`list-workflows`)

`GET /api/v1/workflows`

Returns one page of workflow summaries, newest first. The next page's cursor is in the X-Next-Cursor response header, empty on the last page.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `limit` | query | no | integer | Maximum workflows to return (default 100) |
| `cursor` | query | no | string | Opaque cursor from the previous page's X-Next-Cursor header |
| `tag` | query | no | string | Keep only workflows whose latest revision carries this tag; case is ignored |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `array or null` |
| `default` | Error | `application/problem+json` |

:::code

```sh title="curl"
curl -X GET "$KILASFLOW_HOST/api/v1/workflows" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const workflows = await client.listWorkflows();
```

```go title="Go"
resp, err := http.Get(baseURL + "/api/v1/workflows?limit=100")
```

:::

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Get a workflow (`get-workflow`)

`GET /api/v1/workflows/{id}`

Returns the current canonical document and lifecycle metadata.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

Fields of the `WorkflowResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | — |
| `activeVersion` | no | `WorkflowVersionResource` | — |
| `activeVersion.createdAt` | yes | `string` | — |
| `activeVersion.document` | yes | `Document` | — |
| `activeVersion.document.connections` | yes | `array or null` | — |
| `activeVersion.document.id` | yes | `string` | — |
| `activeVersion.document.name` | yes | `string` | — |
| `activeVersion.document.nodes` | yes | `array or null` | — |
| `activeVersion.document.pinData` | no | `object` | — |
| `activeVersion.document.schemaVersion` | yes | `integer` | — |
| `activeVersion.document.settings` | yes | `object` | — |
| `activeVersion.document.tags` | no | `array or null` | — |
| `activeVersion.id` | yes | `string` | — |
| `activeVersion.revision` | yes | `integer` | — |
| `activeVersion.schemaVersion` | yes | `integer` | — |
| `activeVersion.workflowId` | yes | `string` | — |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `latestVersion` | yes | `WorkflowVersionResource` | — |
| `latestVersion.createdAt` | yes | `string` | — |
| `latestVersion.document` | yes | `Document` | — |
| `latestVersion.document.connections` | yes | `array or null` | — |
| `latestVersion.document.id` | yes | `string` | — |
| `latestVersion.document.name` | yes | `string` | — |
| `latestVersion.document.nodes` | yes | `array or null` | — |
| `latestVersion.document.pinData` | no | `object` | — |
| `latestVersion.document.schemaVersion` | yes | `integer` | — |
| `latestVersion.document.settings` | yes | `object` | — |
| `latestVersion.document.tags` | no | `array or null` | — |
| `latestVersion.id` | yes | `string` | — |
| `latestVersion.revision` | yes | `integer` | — |
| `latestVersion.schemaVersion` | yes | `integer` | — |
| `latestVersion.workflowId` | yes | `string` | — |
| `name` | yes | `string` | — |
| `updatedAt` | yes | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "active": false,
  "activeVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "latestVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "name": "string",
  "updatedAt": "2026-01-02T15:04:05Z"
}
```

:::code

```sh title="curl"
curl -X GET "$KILASFLOW_HOST/api/v1/workflows/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const workflow = await client.getWorkflow(workflowId);
```

```go title="Go"
resp, err := http.Get(baseURL + "/api/v1/workflows/" + url.PathEscape(workflowId))
```

:::

Embed: Allow with `workflow:read` — on the session’s workflow only.

## List a workflow's webhook URLs (`list-workflow-webhooks`)

`GET /api/v1/workflows/{id}/webhooks`

Returns every webhook trigger's public address. The opaque route is minted on first read and reused forever, so the URL is known before activation and unchanged by it.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `array or null` |
| `default` | Error | `application/problem+json` |

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/workflows/{id}/webhooks" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:read` — on the session’s workflow only.

## Save a workflow draft (`update-workflow`)

`PUT /api/v1/workflows/{id}`

Appends an immutable revision, including incomplete drafts.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `If-Match` | header | no | string | Latest version ID the editor saved from; a save from a stale revision is refused with 409 |
| `X-KilasFlow-Skills-Used` | header | no | string | Comma-separated names of the skills an agent used to make this call, recorded on the revision it creates |

Request body: `application/json` — `WorkflowDocumentInput` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `baseVersionId` | no | `string` | Latest version ID the editor saved from; a save from a stale revision is refused with 409 |
| `connections` | yes | `array or null` | — |
| `name` | yes | `string` | — |
| `nodes` | yes | `array or null` | — |
| `pinData` | no | `object` | Node output the author pinned, keyed by node name: a run feeds a node's pinned items as its output instead of executing the node |
| `schemaVersion` | yes | `integer` | — |
| `settings` | yes | `object` | — |
| `tags` | no | `array or null` | Optional labels that group workflows. At most 20; each 1 to 24 characters with no leading or trailing whitespace, no comma and no control character; unique ignoring case. Order and case are kept. The list filters by one with ?tag= |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

Fields of the `WorkflowResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | — |
| `activeVersion` | no | `WorkflowVersionResource` | — |
| `activeVersion.createdAt` | yes | `string` | — |
| `activeVersion.document` | yes | `Document` | — |
| `activeVersion.document.connections` | yes | `array or null` | — |
| `activeVersion.document.id` | yes | `string` | — |
| `activeVersion.document.name` | yes | `string` | — |
| `activeVersion.document.nodes` | yes | `array or null` | — |
| `activeVersion.document.pinData` | no | `object` | — |
| `activeVersion.document.schemaVersion` | yes | `integer` | — |
| `activeVersion.document.settings` | yes | `object` | — |
| `activeVersion.document.tags` | no | `array or null` | — |
| `activeVersion.id` | yes | `string` | — |
| `activeVersion.revision` | yes | `integer` | — |
| `activeVersion.schemaVersion` | yes | `integer` | — |
| `activeVersion.workflowId` | yes | `string` | — |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `latestVersion` | yes | `WorkflowVersionResource` | — |
| `latestVersion.createdAt` | yes | `string` | — |
| `latestVersion.document` | yes | `Document` | — |
| `latestVersion.document.connections` | yes | `array or null` | — |
| `latestVersion.document.id` | yes | `string` | — |
| `latestVersion.document.name` | yes | `string` | — |
| `latestVersion.document.nodes` | yes | `array or null` | — |
| `latestVersion.document.pinData` | no | `object` | — |
| `latestVersion.document.schemaVersion` | yes | `integer` | — |
| `latestVersion.document.settings` | yes | `object` | — |
| `latestVersion.document.tags` | no | `array or null` | — |
| `latestVersion.id` | yes | `string` | — |
| `latestVersion.revision` | yes | `integer` | — |
| `latestVersion.schemaVersion` | yes | `integer` | — |
| `latestVersion.workflowId` | yes | `string` | — |
| `name` | yes | `string` | — |
| `updatedAt` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "connections": [
    {
      "id": "string",
      "kind": "string",
      "source": {
        "nodeId": "string",
        "port": "string"
      },
      "target": {
        "nodeId": "string",
        "port": "string"
      }
    }
  ],
  "name": "string",
  "nodes": [
    {
      "credentials": {},
      "disabled": false,
      "id": "string",
      "name": "string",
      "parameters": {},
      "position": {
        "x": 0,
        "y": 0
      },
      "settings": {},
      "type": "string",
      "typeVersion": 1
    }
  ],
  "schemaVersion": 0,
  "settings": {}
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "active": false,
  "activeVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "latestVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "name": "string",
  "updatedAt": "2026-01-02T15:04:05Z"
}
```

:::code

```sh title="curl"
curl -X PUT "$KILASFLOW_HOST/api/v1/workflows/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"connections":[{"id":"string","kind":"string","source":{"nodeId":"string","port":"string"},"target":{"nodeId":"string","port":"string"}}],"name":"string","nodes":[{"credentials":{},"disabled":false,"id":"string","name":"string","parameters":{},"position":{"x":0,"y":0},"settings":{},"type":"string","typeVersion":1}],"schemaVersion":0,"settings":{}}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const workflow = await client.updateWorkflow(workflowId, document);
```

```go title="Go"
payload, _ := json.Marshal(document)
req, _ := http.NewRequest(http.MethodPut, baseURL+"/api/v1/workflows/"+url.PathEscape(workflowId), bytes.NewReader(payload))
req.Header.Set("Content-Type", "application/json")
resp, err := http.DefaultClient.Do(req)
```

:::

Embed: Allow with `workflow:write` — on the session’s workflow only.

## Delete a workflow (`delete-workflow`)

`DELETE /api/v1/workflows/{id}`

Soft-deletes a workflow while retaining audit evidence.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `204` | No Content |  |
| `default` | Error | `application/problem+json` |

```sh
curl -X DELETE "$KILASFLOW_HOST/api/v1/workflows/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — an embed session cannot delete a workflow.

## Queue a manual workflow run (`run-workflow`)

`POST /api/v1/workflows/{id}/run`

Validates and queues a saved revision without requiring activation. Body.workflowVersionId pins the revision to run; omit it to run the latest. Body.triggerNodeId selects the trigger to start from; omit it to run every trigger, and a node that cannot start a run is refused with 422. Send Idempotency-Key to make a retry safe: 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `Idempotency-Key` | header | no | string | 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency. |

Request body: `application/json` — `RunWorkflowInputBody`

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `input` | no |  | Optional manual-run input JSON |
| `triggerNodeId` | no | `string` | Trigger node this manual run starts from. Omit to run every trigger. |
| `workflowVersionId` | no | `string` | Revision to run. Omit to run the workflow's latest revision. |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `202` | Accepted | `application/json` — `ExecutionRequestResource` |
| `default` | Error | `application/problem+json` |

Fields of the `ExecutionRequestResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `input` | no |  | — |
| `status` | yes | `string` | — |
| `trigger` | yes | `string` | — |
| `triggerNodeId` | no | `string` | — |
| `workflowId` | yes | `string` | — |
| `workflowVersionId` | yes | `string` | — |

Request (minimal — required fields only):

```json
{}
```

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "input": null,
  "status": "string",
  "trigger": "string",
  "triggerNodeId": "string",
  "workflowId": "string",
  "workflowVersionId": "string"
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/workflows/{id}/run" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{}'
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const { id: executionId } = await client.runWorkflow(workflowId, input);
```

```go title="Go"
payload, _ := json.Marshal(map[string]any{"input": input})
req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/workflows/"+url.PathEscape(workflowId)+"/run", bytes.NewReader(payload))
resp, err := http.DefaultClient.Do(req) // 202: follow the execution on the event stream
```

:::

Embed: Allow with `workflow:run` — on the session’s workflow only.

## Duplicate a workflow (`duplicate-workflow`)

`POST /api/v1/workflows/{id}/duplicate`

Copies a workflow's latest revision into a new workflow of the same tenant, named after the source with (copy) after it unless the request names one.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `X-KilasFlow-Skills-Used` | header | no | string | Comma-separated names of the skills an agent used to make this call, recorded on the revision it creates |

Request body: `application/json` — `DuplicateWorkflowInputBody`

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `name` | no | `string` | Name for the copy; defaults to the source name followed by (copy) |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

Fields of the `WorkflowResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | — |
| `activeVersion` | no | `WorkflowVersionResource` | — |
| `activeVersion.createdAt` | yes | `string` | — |
| `activeVersion.document` | yes | `Document` | — |
| `activeVersion.document.connections` | yes | `array or null` | — |
| `activeVersion.document.id` | yes | `string` | — |
| `activeVersion.document.name` | yes | `string` | — |
| `activeVersion.document.nodes` | yes | `array or null` | — |
| `activeVersion.document.pinData` | no | `object` | — |
| `activeVersion.document.schemaVersion` | yes | `integer` | — |
| `activeVersion.document.settings` | yes | `object` | — |
| `activeVersion.document.tags` | no | `array or null` | — |
| `activeVersion.id` | yes | `string` | — |
| `activeVersion.revision` | yes | `integer` | — |
| `activeVersion.schemaVersion` | yes | `integer` | — |
| `activeVersion.workflowId` | yes | `string` | — |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `latestVersion` | yes | `WorkflowVersionResource` | — |
| `latestVersion.createdAt` | yes | `string` | — |
| `latestVersion.document` | yes | `Document` | — |
| `latestVersion.document.connections` | yes | `array or null` | — |
| `latestVersion.document.id` | yes | `string` | — |
| `latestVersion.document.name` | yes | `string` | — |
| `latestVersion.document.nodes` | yes | `array or null` | — |
| `latestVersion.document.pinData` | no | `object` | — |
| `latestVersion.document.schemaVersion` | yes | `integer` | — |
| `latestVersion.document.settings` | yes | `object` | — |
| `latestVersion.document.tags` | no | `array or null` | — |
| `latestVersion.id` | yes | `string` | — |
| `latestVersion.revision` | yes | `integer` | — |
| `latestVersion.schemaVersion` | yes | `integer` | — |
| `latestVersion.workflowId` | yes | `string` | — |
| `name` | yes | `string` | — |
| `updatedAt` | yes | `string` | — |

Request (minimal — required fields only):

```json
{}
```

Response (skeleton — every field, placeholder values):

```json
{
  "active": false,
  "activeVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "latestVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "name": "string",
  "updatedAt": "2026-01-02T15:04:05Z"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/workflows/{id}/duplicate" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{}'
```

Embed: Deny — a copy is a new workflow, outside any session’s single-workflow authority.

## Activate latest workflow revision (`activate-workflow`)

`POST /api/v1/workflows/{id}/activate`

Compiles the latest revision before pinning it as active.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ActivationResource` |
| `default` | Error | `application/problem+json` |

Fields of the `ActivationResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | — |
| `activeVersion` | no | `WorkflowVersionResource` | — |
| `activeVersion.createdAt` | yes | `string` | — |
| `activeVersion.document` | yes | `Document` | — |
| `activeVersion.document.connections` | yes | `array or null` | — |
| `activeVersion.document.id` | yes | `string` | — |
| `activeVersion.document.name` | yes | `string` | — |
| `activeVersion.document.nodes` | yes | `array or null` | — |
| `activeVersion.document.pinData` | no | `object` | — |
| `activeVersion.document.schemaVersion` | yes | `integer` | — |
| `activeVersion.document.settings` | yes | `object` | — |
| `activeVersion.document.tags` | no | `array or null` | — |
| `activeVersion.id` | yes | `string` | — |
| `activeVersion.revision` | yes | `integer` | — |
| `activeVersion.schemaVersion` | yes | `integer` | — |
| `activeVersion.workflowId` | yes | `string` | — |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `latestVersion` | yes | `WorkflowVersionResource` | — |
| `latestVersion.createdAt` | yes | `string` | — |
| `latestVersion.document` | yes | `Document` | — |
| `latestVersion.document.connections` | yes | `array or null` | — |
| `latestVersion.document.id` | yes | `string` | — |
| `latestVersion.document.name` | yes | `string` | — |
| `latestVersion.document.nodes` | yes | `array or null` | — |
| `latestVersion.document.pinData` | no | `object` | — |
| `latestVersion.document.schemaVersion` | yes | `integer` | — |
| `latestVersion.document.settings` | yes | `object` | — |
| `latestVersion.document.tags` | no | `array or null` | — |
| `latestVersion.id` | yes | `string` | — |
| `latestVersion.revision` | yes | `integer` | — |
| `latestVersion.schemaVersion` | yes | `integer` | — |
| `latestVersion.workflowId` | yes | `string` | — |
| `name` | yes | `string` | — |
| `notices` | yes | `array or null` | — |
| `updatedAt` | yes | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "active": false,
  "activeVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "latestVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "name": "string",
  "notices": [
    {
      "message": "string",
      "nodeId": "string",
      "nodeType": "string"
    }
  ],
  "updatedAt": "2026-01-02T15:04:05Z"
}
```

:::code

```sh title="curl"
curl -X POST "$KILASFLOW_HOST/api/v1/workflows/{id}/activate" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
await client.activateWorkflow(workflowId);
```

```go title="Go"
resp, err := http.Post(baseURL+"/api/v1/workflows/"+url.PathEscape(workflowId)+"/activate", "application/json", nil)
```

:::

Embed: Allow with `workflow:activate` only — on the session’s workflow, through the same compile and catalogue checks as the dashboard. No other scope grants it: activation registers deployment-wide triggers, so a host opts in by minting the scope.

## Deactivate workflow (`deactivate-workflow`)

`POST /api/v1/workflows/{id}/deactivate`

Idempotently disables trigger execution while retaining published history.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

Fields of the `WorkflowResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | — |
| `activeVersion` | no | `WorkflowVersionResource` | — |
| `activeVersion.createdAt` | yes | `string` | — |
| `activeVersion.document` | yes | `Document` | — |
| `activeVersion.document.connections` | yes | `array or null` | — |
| `activeVersion.document.id` | yes | `string` | — |
| `activeVersion.document.name` | yes | `string` | — |
| `activeVersion.document.nodes` | yes | `array or null` | — |
| `activeVersion.document.pinData` | no | `object` | — |
| `activeVersion.document.schemaVersion` | yes | `integer` | — |
| `activeVersion.document.settings` | yes | `object` | — |
| `activeVersion.document.tags` | no | `array or null` | — |
| `activeVersion.id` | yes | `string` | — |
| `activeVersion.revision` | yes | `integer` | — |
| `activeVersion.schemaVersion` | yes | `integer` | — |
| `activeVersion.workflowId` | yes | `string` | — |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `latestVersion` | yes | `WorkflowVersionResource` | — |
| `latestVersion.createdAt` | yes | `string` | — |
| `latestVersion.document` | yes | `Document` | — |
| `latestVersion.document.connections` | yes | `array or null` | — |
| `latestVersion.document.id` | yes | `string` | — |
| `latestVersion.document.name` | yes | `string` | — |
| `latestVersion.document.nodes` | yes | `array or null` | — |
| `latestVersion.document.pinData` | no | `object` | — |
| `latestVersion.document.schemaVersion` | yes | `integer` | — |
| `latestVersion.document.settings` | yes | `object` | — |
| `latestVersion.document.tags` | no | `array or null` | — |
| `latestVersion.id` | yes | `string` | — |
| `latestVersion.revision` | yes | `integer` | — |
| `latestVersion.schemaVersion` | yes | `integer` | — |
| `latestVersion.workflowId` | yes | `string` | — |
| `name` | yes | `string` | — |
| `updatedAt` | yes | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "active": false,
  "activeVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "latestVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "name": "string",
  "updatedAt": "2026-01-02T15:04:05Z"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/workflows/{id}/deactivate" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:activate` only — on the session’s workflow. `workflow:publish` does not grant it: taking a workflow offline is the activation switch.

## List workflow revisions (`list-workflow-versions`)

`GET /api/v1/workflows/{id}/versions`

Returns one page of a workflow's version history, newest first. Summaries carry no document.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `limit` | query | no | integer | Maximum revisions to return (default 25) |
| `cursor` | query | no | string | Opaque cursor from a previous listing's nextCursor |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowVersionListResource` |
| `default` | Error | `application/problem+json` |

Fields of the `WorkflowVersionListResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `items` | yes | `array or null` | — |
| `nextCursor` | no | `string` | Pass back as ?cursor= to read the next page |

Response (skeleton — every field, placeholder values):

```json
{
  "items": [
    {
      "actorKeyId": "string",
      "actorKind": "user",
      "actorLabel": "string",
      "actorMeta": [
        "string"
      ],
      "createdAt": "2026-01-02T15:04:05Z",
      "createdBy": "string",
      "draft": false,
      "id": "string",
      "label": "string",
      "published": false,
      "revision": 0,
      "schemaVersion": 0,
      "workflowId": "string"
    }
  ],
  "nextCursor": "string"
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/workflows/{id}/versions" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:read` — on the session’s workflow only.

## Get one workflow revision (`get-workflow-version`)

`GET /api/v1/workflows/{id}/versions/{versionId}`

Returns an immutable revision by ID, so an execution inspector can replay the exact graph that ran.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `versionId` | path | yes | string | Workflow revision identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowVersionResource` |
| `default` | Error | `application/problem+json` |

Fields of the `WorkflowVersionResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `createdAt` | yes | `string` | — |
| `document` | yes | `Document` | — |
| `document.connections` | yes | `array or null` | — |
| `document.id` | yes | `string` | — |
| `document.name` | yes | `string` | — |
| `document.nodes` | yes | `array or null` | — |
| `document.pinData` | no | `object` | — |
| `document.schemaVersion` | yes | `integer` | — |
| `document.settings` | yes | `object` | — |
| `document.tags` | no | `array or null` | — |
| `id` | yes | `string` | — |
| `revision` | yes | `integer` | — |
| `schemaVersion` | yes | `integer` | — |
| `workflowId` | yes | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "createdAt": "2026-01-02T15:04:05Z",
  "document": {
    "connections": [
      {
        "id": "string",
        "kind": "string",
        "source": {},
        "target": {}
      }
    ],
    "id": "string",
    "name": "string",
    "nodes": [
      {
        "credentials": {},
        "disabled": false,
        "id": "string",
        "name": "string",
        "parameters": {},
        "position": {},
        "settings": {},
        "type": "string",
        "typeVersion": 1
      }
    ],
    "pinData": {},
    "schemaVersion": 0,
    "settings": {},
    "tags": [
      "string"
    ]
  },
  "id": "string",
  "revision": 0,
  "schemaVersion": 0,
  "workflowId": "string"
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/workflows/{id}/versions/{versionId}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:read` — on the session’s workflow only.

## Publish one workflow revision (`publish-workflow-version`)

`POST /api/v1/workflows/{id}/versions/{versionId}/publish`

Compiles the named revision and pins it as the version production traffic runs, which is how a bad save is rolled back.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `versionId` | path | yes | string | Workflow revision identifier |
| `X-KilasFlow-Skills-Used` | header | no | string | Comma-separated names of the skills an agent used to make this call, recorded on the revision it creates |

Request body: `application/json` — `PublishVersionInputBody`

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `reason` | no | `string` | Why this revision was published or restored, recorded in the audit trail |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

Fields of the `WorkflowResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | — |
| `activeVersion` | no | `WorkflowVersionResource` | — |
| `activeVersion.createdAt` | yes | `string` | — |
| `activeVersion.document` | yes | `Document` | — |
| `activeVersion.document.connections` | yes | `array or null` | — |
| `activeVersion.document.id` | yes | `string` | — |
| `activeVersion.document.name` | yes | `string` | — |
| `activeVersion.document.nodes` | yes | `array or null` | — |
| `activeVersion.document.pinData` | no | `object` | — |
| `activeVersion.document.schemaVersion` | yes | `integer` | — |
| `activeVersion.document.settings` | yes | `object` | — |
| `activeVersion.document.tags` | no | `array or null` | — |
| `activeVersion.id` | yes | `string` | — |
| `activeVersion.revision` | yes | `integer` | — |
| `activeVersion.schemaVersion` | yes | `integer` | — |
| `activeVersion.workflowId` | yes | `string` | — |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `latestVersion` | yes | `WorkflowVersionResource` | — |
| `latestVersion.createdAt` | yes | `string` | — |
| `latestVersion.document` | yes | `Document` | — |
| `latestVersion.document.connections` | yes | `array or null` | — |
| `latestVersion.document.id` | yes | `string` | — |
| `latestVersion.document.name` | yes | `string` | — |
| `latestVersion.document.nodes` | yes | `array or null` | — |
| `latestVersion.document.pinData` | no | `object` | — |
| `latestVersion.document.schemaVersion` | yes | `integer` | — |
| `latestVersion.document.settings` | yes | `object` | — |
| `latestVersion.document.tags` | no | `array or null` | — |
| `latestVersion.id` | yes | `string` | — |
| `latestVersion.revision` | yes | `integer` | — |
| `latestVersion.schemaVersion` | yes | `integer` | — |
| `latestVersion.workflowId` | yes | `string` | — |
| `name` | yes | `string` | — |
| `updatedAt` | yes | `string` | — |

Request (minimal — required fields only):

```json
{}
```

Response (skeleton — every field, placeholder values):

```json
{
  "active": false,
  "activeVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "latestVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "name": "string",
  "updatedAt": "2026-01-02T15:04:05Z"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/workflows/{id}/versions/{versionId}/publish" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{}'
```

Embed: Allow with `workflow:publish` only — on the session’s workflow; `workflow:write` alone is refused. Publishing pins the revision production traffic runs, so a host opts in by minting the scope.

## Restore one workflow revision (`restore-workflow-version`)

`POST /api/v1/workflows/{id}/versions/{versionId}/restore`

Appends a new revision carrying an older snapshot's document. History is append-only: the restored-from revision is left unchanged.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `versionId` | path | yes | string | Workflow revision identifier |
| `X-KilasFlow-Skills-Used` | header | no | string | Comma-separated names of the skills an agent used to make this call, recorded on the revision it creates |

Request body: `application/json` — `PublishVersionInputBody`

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `reason` | no | `string` | Why this revision was published or restored, recorded in the audit trail |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

Fields of the `WorkflowResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `active` | yes | `boolean` | — |
| `activeVersion` | no | `WorkflowVersionResource` | — |
| `activeVersion.createdAt` | yes | `string` | — |
| `activeVersion.document` | yes | `Document` | — |
| `activeVersion.document.connections` | yes | `array or null` | — |
| `activeVersion.document.id` | yes | `string` | — |
| `activeVersion.document.name` | yes | `string` | — |
| `activeVersion.document.nodes` | yes | `array or null` | — |
| `activeVersion.document.pinData` | no | `object` | — |
| `activeVersion.document.schemaVersion` | yes | `integer` | — |
| `activeVersion.document.settings` | yes | `object` | — |
| `activeVersion.document.tags` | no | `array or null` | — |
| `activeVersion.id` | yes | `string` | — |
| `activeVersion.revision` | yes | `integer` | — |
| `activeVersion.schemaVersion` | yes | `integer` | — |
| `activeVersion.workflowId` | yes | `string` | — |
| `createdAt` | yes | `string` | — |
| `id` | yes | `string` | — |
| `latestVersion` | yes | `WorkflowVersionResource` | — |
| `latestVersion.createdAt` | yes | `string` | — |
| `latestVersion.document` | yes | `Document` | — |
| `latestVersion.document.connections` | yes | `array or null` | — |
| `latestVersion.document.id` | yes | `string` | — |
| `latestVersion.document.name` | yes | `string` | — |
| `latestVersion.document.nodes` | yes | `array or null` | — |
| `latestVersion.document.pinData` | no | `object` | — |
| `latestVersion.document.schemaVersion` | yes | `integer` | — |
| `latestVersion.document.settings` | yes | `object` | — |
| `latestVersion.document.tags` | no | `array or null` | — |
| `latestVersion.id` | yes | `string` | — |
| `latestVersion.revision` | yes | `integer` | — |
| `latestVersion.schemaVersion` | yes | `integer` | — |
| `latestVersion.workflowId` | yes | `string` | — |
| `name` | yes | `string` | — |
| `updatedAt` | yes | `string` | — |

Request (minimal — required fields only):

```json
{}
```

Response (skeleton — every field, placeholder values):

```json
{
  "active": false,
  "activeVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "createdAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "latestVersion": {
    "createdAt": "2026-01-02T15:04:05Z",
    "document": {
      "connections": [
        {}
      ],
      "id": "string",
      "name": "string",
      "nodes": [
        {}
      ],
      "pinData": {},
      "schemaVersion": 0,
      "settings": {},
      "tags": [
        "string"
      ]
    },
    "id": "string",
    "revision": 0,
    "schemaVersion": 0,
    "workflowId": "string"
  },
  "name": "string",
  "updatedAt": "2026-01-02T15:04:05Z"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/workflows/{id}/versions/{versionId}/restore" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{}'
```

Embed: Allow with `workflow:write` — on the session’s workflow only.

## List a workflow's publish history (`list-workflow-publish-events`)

`GET /api/v1/workflows/{id}/publish-events`

Returns every publish, unpublish and restore recorded for a workflow, newest first.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `array or null` |
| `default` | Error | `application/problem+json` |

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/workflows/{id}/publish-events" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:read` — on the session’s workflow only.

## Read a revision's import report (`workflow-diagnostics`)

`GET /api/v1/workflows/{id}/diagnostics`

Returns the report stored with a revision when an import created it: what the n8n translation could not carry faithfully, per node and per field. A revision that was not imported answers with no source and no issues.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `versionId` | query | no | string | Revision to read; defaults to the newest |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowDiagnosticsResource` |
| `default` | Error | `application/problem+json` |

Fields of the `WorkflowDiagnosticsResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `importedAt` | no | `string` | — |
| `issues` | yes | `array or null` | — |
| `revision` | yes | `integer` | — |
| `source` | no | `string` | — |
| `versionId` | yes | `string` | — |
| `workflowId` | yes | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "importedAt": "2026-01-02T15:04:05Z",
  "issues": [
    {
      "field": "string",
      "nodeId": "string",
      "nodeName": "string",
      "reason": "string",
      "severity": "blocking",
      "type": "string",
      "typeVersion": 1
    }
  ],
  "revision": 0,
  "source": "string",
  "versionId": "string",
  "workflowId": "string"
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/workflows/{id}/diagnostics" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:read` — on the session’s workflow only.
