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

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `ImportedWorkflowResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — importing creates a new workflow, outside any session’s single-workflow authority.

## Convert pasted n8n nodes (`convert-workflow-fragment`)

`POST /api/v1/workflows/convert`

Translates n8n JSON — nodes copied from an n8n canvas, or a whole export — into canonical nodes and connections with the same translator import uses, and answers them with the import report, saving nothing. It is what the editor calls when n8n nodes are pasted onto the canvas.

Request body: `application/json` — `ConvertFragmentInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ConvertedFragmentResource` |
| `default` | Error | `application/problem+json` |

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

Embed: Allow with `workflow:read` — on the session’s workflow only.
