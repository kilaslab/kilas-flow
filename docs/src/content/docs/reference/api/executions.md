---
title: Executions
description: "List, inspect, cancel, retry, and follow executions on the event stream. Generated from the live OpenAPI document."
sidebar:
  order: 2
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

List, inspect, cancel, retry, and follow executions on the event stream.

## List workflow executions (`list-executions`)

`GET /api/v1/executions`

Returns one page of execution history, newest first.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `workflowId` | query | no | string | Only list executions of this workflow |
| `status` | query | no | array or null | Only list executions in these statuses |
| `trigger` | query | no | string | Only list executions started by this trigger |
| `limit` | query | no | integer | Maximum executions to return (default 25) |
| `cursor` | query | no | string | Opaque cursor from a previous listing's nextCursor |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ExecutionListResource` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:read` — the query must name the session’s workflow (`workflowId`).

## Get a workflow execution (`get-execution`)

`GET /api/v1/executions/{id}`

Returns durable execution state and its ordered node-run trace.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Execution identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ExecutionResource` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:read` — ownership is checked in the handler, which alone can know which workflow an execution belongs to.

## Cancel a workflow execution (`cancel-execution`)

`POST /api/v1/executions/{id}/cancel`

Requests cancellation of queued or running work.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Execution identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `202` | Accepted | `application/json` — `ExecutionRequestResource` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:read` — ownership is checked in the handler, which alone can know which workflow an execution belongs to.

## Retry a finished execution (`retry-execution`)

`POST /api/v1/executions/{id}/retry`

Starts a new execution from a finished one's workflow, revision and input — the revision that ran, not the workflow's newest — under the trigger the original ran under, so a retried webhook run is a webhook run again. An execution that is still queued or running is refused with 409, and so is one waiting on an approval: retrying it would run the same input beside itself.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Execution identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `ExecutionResource` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:read` — ownership is checked in the handler, which alone can know which workflow an execution belongs to.

## Evaluate an expression against an execution (`eval-expression`)

`POST /api/v1/executions/{id}/eval`

Evaluates one expression against the node outputs an execution's trace already stores, under the budget a node of that revision is given, and answers the value and its JSON shape. Nothing is written and nothing runs: this reads what a field held while the workflow ran. The grammar is the one every workflow document is already evaluated with, and $env is the runtime's allowlist rather than the process environment.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Execution identifier |

Request body: `application/json` — `EvalExpressionInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `EvalExpressionResource` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:read` — ownership is checked in the handler, which alone can know which workflow an execution belongs to.

## Stream execution events (`stream-execution-events`)

`GET /api/v1/executions/{id}/events`

Live standardized event feed for one execution. Replays retained events after Last-Event-ID, then streams until the execution reaches a terminal state. A run that has already finished answers 404 when its record is gone, and otherwise replays its outcome and closes.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Execution identifier |
| `Last-Event-ID` | header | no | string | Resume after this event ID |
| `from` | query | no | integer | Resume after this event ID when a header cannot be set |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `text/event-stream` |
| `default` | Error | `application/problem+json` |

Embed: Allow with `workflow:read` — ownership is checked in the handler, which alone can know which workflow an execution belongs to.
