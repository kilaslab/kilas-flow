---
title: Node types
description: "The node catalogue, icons, load-options, load-schema, and the expression grammar. Generated from the live OpenAPI document."
sidebar:
  order: 6
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

Embed: Allow with `workflow:read` — the catalogue is narrowed to the session’s own tenant. `load-options` is additionally bounded to the session’s workflow by the handler.

## Load a property's selectable values (`load-node-property-options`)

`POST /api/v1/node-types/{type}/load-options`

Resolves the options for a property whose valid values live on the customer's own service. The loader is taken from the registered definition, never from the request. A node type the caller's tenant cannot see answers 404 exactly as an unregistered one does.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `type` | path | yes | string |  |

Request body: `application/json` — `LoadOptionsInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `LoadOptionsResource` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:read` — the catalogue is narrowed to the session’s own tenant. `load-options` is additionally bounded to the session’s workflow by the handler.

## Load a resource mapper's columns (`load-node-property-schema`)

`POST /api/v1/node-types/{type}/load-schema`

Resolves the column list a resource mapper maps onto, with each column's type, required flag and match eligibility. A sibling of load-options rather than a widening of it: an option is {label, value} and a column is not. A node type the caller's tenant cannot see answers 404 exactly as an unregistered one does.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `type` | path | yes | string |  |

Request body: `application/json` — `LoadOptionsInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `LoadSchemaResource` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:read` — the catalogue is narrowed to the session’s own tenant. `load-options` is additionally bounded to the session’s workflow by the handler.

## Describe the expression grammar (`get-expression-grammar`)

`GET /api/v1/expression-grammar`

Returns the roots and functions an expression may use, so the editor validates against the server rather than a copy that drifts from it.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ExpressionGrammar` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
