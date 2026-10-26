---
title: Interop
description: "Import and export workflows across formats. Generated from the live OpenAPI document."
sidebar:
  order: 9
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Import and export workflows across formats.

## Import an n8n workflow (`import-workflow`)

`POST /api/v1/workflows/import`

Translates n8n workflow JSON into a KilasFlow draft. Unsupported nodes are imported as visible placeholders that block activation rather than being dropped or silently remapped.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `X-KilasFlow-Skills-Used` | header | no | string | Comma-separated names of the skills an agent used to make this call, recorded on the revision it creates |

Request body: `application/json` — `ImportWorkflowInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `format` | no | `string` | Only "n8n" is supported |
| `name` | no | `string` | Overrides the imported workflow's name |
| `workflow` | yes |  | An n8n workflow JSON document |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `ImportedWorkflowResource` |
| `default` | Error | `application/problem+json` |

Fields of the `ImportedWorkflowResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `unsupported` | yes | `array or null` | — |
| `webhooks` | yes | `array or null` | — |
| `workflow` | yes | `WorkflowResource` | — |
| `workflow.active` | yes | `boolean` | — |
| `workflow.activeVersion` | no | `WorkflowVersionResource` | — |
| `workflow.activeVersion.createdAt` | yes | `string` | — |
| `workflow.activeVersion.document` | yes | `Document` | — |
| `workflow.activeVersion.document.connections` | yes | `array or null` | — |
| `workflow.activeVersion.document.id` | yes | `string` | — |
| `workflow.activeVersion.document.name` | yes | `string` | — |
| `workflow.activeVersion.document.nodes` | yes | `array or null` | — |
| `workflow.activeVersion.document.pinData` | no | `object` | — |
| `workflow.activeVersion.document.schemaVersion` | yes | `integer` | — |
| `workflow.activeVersion.document.settings` | yes | `object` | — |
| `workflow.activeVersion.document.tags` | no | `array or null` | — |
| `workflow.activeVersion.id` | yes | `string` | — |
| `workflow.activeVersion.revision` | yes | `integer` | — |
| `workflow.activeVersion.schemaVersion` | yes | `integer` | — |
| `workflow.activeVersion.workflowId` | yes | `string` | — |
| `workflow.createdAt` | yes | `string` | — |
| `workflow.id` | yes | `string` | — |
| `workflow.latestVersion` | yes | `WorkflowVersionResource` | — |
| `workflow.latestVersion.createdAt` | yes | `string` | — |
| `workflow.latestVersion.document` | yes | `Document` | — |
| `workflow.latestVersion.document.connections` | yes | `array or null` | — |
| `workflow.latestVersion.document.id` | yes | `string` | — |
| `workflow.latestVersion.document.name` | yes | `string` | — |
| `workflow.latestVersion.document.nodes` | yes | `array or null` | — |
| `workflow.latestVersion.document.pinData` | no | `object` | — |
| `workflow.latestVersion.document.schemaVersion` | yes | `integer` | — |
| `workflow.latestVersion.document.settings` | yes | `object` | — |
| `workflow.latestVersion.document.tags` | no | `array or null` | — |
| `workflow.latestVersion.id` | yes | `string` | — |
| `workflow.latestVersion.revision` | yes | `integer` | — |
| `workflow.latestVersion.schemaVersion` | yes | `integer` | — |
| `workflow.latestVersion.workflowId` | yes | `string` | — |
| `workflow.name` | yes | `string` | — |
| `workflow.updatedAt` | yes | `string` | — |

Request (minimal — required fields only):

```json
{
  "workflow": null
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "unsupported": [
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
  "webhooks": [
    {
      "method": "string",
      "nodeId": "string",
      "path": "string",
      "url": "https://flows.example"
    }
  ],
  "workflow": {
    "active": false,
    "activeVersion": {
      "createdAt": "2026-01-02T15:04:05Z",
      "document": {},
      "id": "string",
      "revision": 0,
      "schemaVersion": 0,
      "workflowId": "string"
    },
    "createdAt": "2026-01-02T15:04:05Z",
    "id": "string",
    "latestVersion": {
      "createdAt": "2026-01-02T15:04:05Z",
      "document": {},
      "id": "string",
      "revision": 0,
      "schemaVersion": 0,
      "workflowId": "string"
    },
    "name": "string",
    "updatedAt": "2026-01-02T15:04:05Z"
  }
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/workflows/import" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"workflow":null}'
```

Embed: Deny — importing creates a new workflow, outside any session’s single-workflow authority.

## Convert pasted n8n nodes (`convert-workflow-fragment`)

`POST /api/v1/workflows/convert`

Translates n8n JSON — nodes copied from an n8n canvas, or a whole export — into canonical nodes and connections with the same translator import uses, and answers them with the import report, saving nothing. It is what the editor calls when n8n nodes are pasted onto the canvas.

Request body: `application/json` — `ConvertFragmentInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `format` | no | `string` | Only "n8n" is supported |
| `workflow` | yes |  | n8n JSON copied from an n8n canvas, or an n8n workflow export |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ConvertedFragmentResource` |
| `default` | Error | `application/problem+json` |

Fields of the `ConvertedFragmentResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `connections` | yes | `array or null` | — |
| `nodes` | yes | `array or null` | — |
| `unsupported` | yes | `array or null` | — |

Request (minimal — required fields only):

```json
{
  "workflow": null
}
```

Response (skeleton — every field, placeholder values):

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
  "unsupported": [
    {
      "field": "string",
      "nodeId": "string",
      "nodeName": "string",
      "reason": "string",
      "severity": "blocking",
      "type": "string",
      "typeVersion": 1
    }
  ]
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/workflows/convert" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"workflow":null}'
```

Embed: Allow with `workflow:read` — it translates pasted n8n JSON and saves nothing; the paste reaches the workflow only through a save the session’s scopes govern.

## Export a workflow as n8n JSON (`export-workflow`)

`GET /api/v1/workflows/{id}/export`

Converts the latest revision into n8n-compatible JSON and reports what could not be carried.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `format` | query | no | string | Only "n8n" is supported |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ExportedWorkflowResource` |
| `default` | Error | `application/problem+json` |

Fields of the `ExportedWorkflowResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `format` | yes | `string` | — |
| `lossy` | yes | `array or null` | — |
| `supportedMappings` | yes | `array or null` | — |
| `workflow` | yes |  | — |

Response (skeleton — every field, placeholder values):

```json
{
  "format": "string",
  "lossy": [
    {
      "field": "string",
      "nodeName": "string",
      "reason": "string",
      "severity": "blocking"
    }
  ],
  "supportedMappings": [
    "string"
  ],
  "workflow": null
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/workflows/{id}/export" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:read` — on the session’s workflow only.
