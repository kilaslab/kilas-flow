---
title: Node types
description: "The node catalogue, icons, load-options, load-schema, and the expression grammar. Generated from the live OpenAPI document."
sidebar:
  order: 7
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

The node catalogue, icons, load-options, load-schema, and the expression grammar.

## List supported node types (`list-node-types`)

`GET /api/v1/node-types`

Returns the server-defined, versioned node catalogue used by the workflow editor and compiler, narrowed to the caller's tenant. A node type scoped to other tenants is absent, exactly as if it were not registered; an embed session sees what its own tenant sees.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `array or null` |
| `default` | Error | `application/problem+json` |

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/node-types" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:read` — the catalogue is narrowed to the session’s own tenant. `load-options` is additionally bounded to the session’s workflow by the handler.

## Serve a node's icon (`get-node-icon`)

`GET /api/v1/node-types/{type}/icon`

Returns the artwork a node ships. Only a registered node type that declares a served icon answers; everything else is 404. A node type the caller's tenant cannot see answers 404 exactly as an unregistered one does.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `type` | path | yes | string |  |
| `version` | query | no | string | Node type version. Omit for the registered default. |
| `theme` | query | no | string | Which variant to serve. A node shipping one variant serves it for both. |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `string` |
| `default` | Error | `application/problem+json` |

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/node-types/{type}/icon" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:read` — the catalogue is narrowed to the session’s own tenant. `load-options` is additionally bounded to the session’s workflow by the handler.

## Load a property's selectable values (`load-node-property-options`)

`POST /api/v1/node-types/{type}/load-options`

Resolves the options for a property whose valid values live on the customer's own service. The loader is taken from the registered definition, never from the request. A node type the caller's tenant cannot see answers 404 exactly as an unregistered one does.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `type` | path | yes | string |  |

Request body: `application/json` — `LoadOptionsInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `credentialId` | no | `string` | — |
| `mode` | no | `string` | For a resource locator, the mode whose list to load. |
| `parameters` | no | `object` | — |
| `property` | yes | `string` | The property whose options to load, by its key. A property inside a collection or a fixed collection's group is accepted by its own key; a top-level property of the same key wins, and among nested ones the first declared does. |
| `version` | no | `string` | Node type version. Omit for the registered default. |
| `workflowId` | no | `string` | — |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `LoadOptionsResource` |
| `default` | Error | `application/problem+json` |

Fields of the `LoadOptionsResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `options` | yes | `array or null` | — |
| `reason` | no | `string` | Why the list is empty, when it is empty for a reason the user can act on. |

Request (minimal — required fields only):

```json
{
  "property": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "options": [
    {
      "label": "string",
      "value": "string"
    }
  ],
  "reason": "string"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/node-types/{type}/load-options" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"property":"string"}'
```

Embed: Allow with `workflow:read` — the catalogue is narrowed to the session’s own tenant. `load-options` is additionally bounded to the session’s workflow by the handler.

## Load a resource mapper's columns (`load-node-property-schema`)

`POST /api/v1/node-types/{type}/load-schema`

Resolves the column list a resource mapper maps onto, with each column's type, required flag and match eligibility. A sibling of load-options rather than a widening of it: an option is {label, value} and a column is not. A node type the caller's tenant cannot see answers 404 exactly as an unregistered one does.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `type` | path | yes | string |  |

Request body: `application/json` — `LoadOptionsInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `credentialId` | no | `string` | — |
| `mode` | no | `string` | For a resource locator, the mode whose list to load. |
| `parameters` | no | `object` | — |
| `property` | yes | `string` | The property whose options to load, by its key. A property inside a collection or a fixed collection's group is accepted by its own key; a top-level property of the same key wins, and among nested ones the first declared does. |
| `version` | no | `string` | Node type version. Omit for the registered default. |
| `workflowId` | no | `string` | — |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `LoadSchemaResource` |
| `default` | Error | `application/problem+json` |

Fields of the `LoadSchemaResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `fields` | yes | `array or null` | — |
| `reason` | no | `string` | Why the list is empty, when it is empty for a reason the user can act on. |

Request (minimal — required fields only):

```json
{
  "property": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "fields": [
    {
      "canBeUsedToMatch": false,
      "defaultMatch": false,
      "displayName": "string",
      "id": "string",
      "options": [
        {
          "label": "string",
          "value": "string"
        }
      ],
      "readOnly": false,
      "required": false,
      "type": "string"
    }
  ],
  "reason": "string"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/node-types/{type}/load-schema" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"property":"string"}'
```

Embed: Allow with `workflow:read` — the catalogue is narrowed to the session’s own tenant. `load-options` is additionally bounded to the session’s workflow by the handler.

## Describe the expression grammar (`get-expression-grammar`)

`GET /api/v1/expression-grammar`

Returns the roots and functions an expression may use, so the editor validates against the server rather than a copy that drifts from it.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ExpressionGrammar` |
| `default` | Error | `application/problem+json` |

Fields of the `ExpressionGrammar` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `functions` | yes | `array or null` | — |
| `roots` | yes | `array or null` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "functions": [
    "string"
  ],
  "roots": [
    "string"
  ]
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/expression-grammar" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
