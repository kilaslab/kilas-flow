---
title: Workflows
description: Create, read, update, run, activate, and version workflows. Generated from the live OpenAPI document.
sidebar:
  order: 1
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Create, read, update, run, activate, and version workflows.

## Create a workflow draft (`create-workflow`)

`POST /api/v1/workflows`

Creates revision 1 of a canonical workflow document.

Request body: `application/json` — `WorkflowDocumentInput` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## List workflows (`list-workflows`)

`GET /api/v1/workflows`

Lists workflows visible to the current tenant.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `array or null` |
| `default` | Error | `application/problem+json` |

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

Embed: Allow with `workflow:read` — on the session’s workflow only.

## Save a workflow draft (`update-workflow`)

`PUT /api/v1/workflows/{id}`

Appends an immutable revision, including incomplete drafts.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |

Request body: `application/json` — `WorkflowDocumentInput` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

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

Embed: Deny — an embed session cannot delete a workflow.

## Queue a manual workflow run (`run-workflow`)

`POST /api/v1/workflows/{id}/run`

Validates and queues the latest saved revision without requiring activation.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |

Request body: `application/json` — `RunWorkflowInputBody`

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `202` | Accepted | `application/json` — `ExecutionRequestResource` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:run` — on the session’s workflow only.

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

Embed: Deny — activation publishes a deployment-wide endpoint; an owner action, not an embed one.

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

Embed: Deny — activation publishes a deployment-wide endpoint; an owner action, not an embed one.

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

Embed: Allow with `workflow:read` — on the session’s workflow only.

## Publish one workflow revision (`publish-workflow-version`)

`POST /api/v1/workflows/{id}/versions/{versionId}/publish`

Compiles the named revision and pins it as the version production traffic runs, which is how a bad save is rolled back.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `versionId` | path | yes | string | Workflow revision identifier |

Request body: `application/json` — `PublishVersionInputBody`

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:write` — on the session’s workflow only.

## Restore one workflow revision (`restore-workflow-version`)

`POST /api/v1/workflows/{id}/versions/{versionId}/restore`

Appends a new revision carrying an older snapshot's document. History is append-only: the restored-from revision is left unchanged.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Workflow identifier |
| `versionId` | path | yes | string | Workflow revision identifier |

Request body: `application/json` — `PublishVersionInputBody`

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `WorkflowResource` |
| `default` | Error | `application/problem+json` |

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

Embed: Allow with `workflow:read` — on the session’s workflow only.
