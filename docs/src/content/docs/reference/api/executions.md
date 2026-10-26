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

Fields of the `ExecutionListResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `items` | yes | `array or null` | — |
| `nextCursor` | no | `string` | Pass back as ?cursor= to read the next page |

Response (skeleton — every field, placeholder values):

```json
{
  "items": [
    {
      "durationMs": 0,
      "finishedAt": "2026-01-02T15:04:05Z",
      "id": "string",
      "parentExecutionId": "string",
      "startedAt": "2026-01-02T15:04:05Z",
      "status": "string",
      "trigger": "string",
      "triggerNodeId": "string",
      "workflowId": "string",
      "workflowVersionId": "string"
    }
  ],
  "nextCursor": "string"
}
```

:::code

```sh title="curl"
curl -X GET "$KILASFLOW_HOST/api/v1/executions" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
for await (const execution of client.iterateExecutions({ workflowId })) {
  console.log(execution.id, execution.status);
}
```

```go title="Go"
resp, err := http.Get(baseURL + "/api/v1/executions?workflowId=" + url.QueryEscape(workflowId) + "&limit=100")
```

:::

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

Fields of the `ExecutionResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `approvalUrl` | no | `string` | The approval page, for a run waiting on an approval only |
| `cancellationRequestedAt` | no | `string` | — |
| `error` | no |  | — |
| `finishedAt` | no | `string` | — |
| `id` | yes | `string` | — |
| `input` | no |  | — |
| `nodeRuns` | yes | `array or null` | — |
| `output` | no |  | — |
| `parentExecutionId` | no | `string` | The execution that called this one, for a sub-workflow run |
| `resumeAt` | no | `string` | When a run waiting on a timer resumes on its own |
| `resumeUrl` | no | `string` | — |
| `startedAt` | yes | `string` | — |
| `status` | yes | `string` | — |
| `trigger` | yes | `string` | — |
| `triggerNodeId` | no | `string` | The trigger node this run started from, when one workflow declares several |
| `workflowId` | yes | `string` | — |
| `workflowVersionId` | yes | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "approvalUrl": "https://flows.example",
  "cancellationRequestedAt": "2026-01-02T15:04:05Z",
  "error": null,
  "finishedAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "input": null,
  "nodeRuns": [
    {
      "attempt": 0,
      "console": null,
      "error": null,
      "finishedAt": "2026-01-02T15:04:05Z",
      "input": null,
      "nodeId": "string",
      "output": null,
      "response": null,
      "runIndex": 0,
      "sequence": 0,
      "startedAt": "2026-01-02T15:04:05Z",
      "status": "string"
    }
  ],
  "output": null,
  "parentExecutionId": "string",
  "resumeAt": "2026-01-02T15:04:05Z",
  "resumeUrl": "https://flows.example",
  "startedAt": "2026-01-02T15:04:05Z",
  "status": "string",
  "trigger": "string",
  "triggerNodeId": "string",
  "workflowId": "string",
  "workflowVersionId": "string"
}
```

:::code

```sh title="curl"
curl -X GET "$KILASFLOW_HOST/api/v1/executions/{id}" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
const execution = await client.getExecution(executionId);
```

```go title="Go"
resp, err := http.Get(baseURL + "/api/v1/executions/" + url.PathEscape(executionId))
```

:::

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
curl -X POST "$KILASFLOW_HOST/api/v1/executions/{id}/cancel" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

```ts title="TypeScript"
// import { KilasFlowClient } from "@kilasflow/sdk/server";
await client.cancelExecution(executionId);
```

```go title="Go"
req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/executions/"+url.PathEscape(executionId)+"/cancel", nil)
resp, err := http.DefaultClient.Do(req)
```

:::

Embed: Allow with `workflow:read` — ownership is checked in the handler, which alone can know which workflow an execution belongs to.

## Retry a finished execution (`retry-execution`)

`POST /api/v1/executions/{id}/retry`

Starts a new execution from a finished one's workflow, revision and input — the revision that ran, not the workflow's newest — under the trigger the original ran under, so a retried webhook run is a webhook run again; a retried sub-workflow run has no caller, so it runs as a manual one. An execution that is still queued or running is refused with 409, and so is one waiting on an approval: retrying it would run the same input beside itself.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Execution identifier |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `ExecutionResource` |
| `default` | Error | `application/problem+json` |

Fields of the `ExecutionResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `approvalUrl` | no | `string` | The approval page, for a run waiting on an approval only |
| `cancellationRequestedAt` | no | `string` | — |
| `error` | no |  | — |
| `finishedAt` | no | `string` | — |
| `id` | yes | `string` | — |
| `input` | no |  | — |
| `nodeRuns` | yes | `array or null` | — |
| `output` | no |  | — |
| `parentExecutionId` | no | `string` | The execution that called this one, for a sub-workflow run |
| `resumeAt` | no | `string` | When a run waiting on a timer resumes on its own |
| `resumeUrl` | no | `string` | — |
| `startedAt` | yes | `string` | — |
| `status` | yes | `string` | — |
| `trigger` | yes | `string` | — |
| `triggerNodeId` | no | `string` | The trigger node this run started from, when one workflow declares several |
| `workflowId` | yes | `string` | — |
| `workflowVersionId` | yes | `string` | — |

Response (skeleton — every field, placeholder values):

```json
{
  "approvalUrl": "https://flows.example",
  "cancellationRequestedAt": "2026-01-02T15:04:05Z",
  "error": null,
  "finishedAt": "2026-01-02T15:04:05Z",
  "id": "string",
  "input": null,
  "nodeRuns": [
    {
      "attempt": 0,
      "console": null,
      "error": null,
      "finishedAt": "2026-01-02T15:04:05Z",
      "input": null,
      "nodeId": "string",
      "output": null,
      "response": null,
      "runIndex": 0,
      "sequence": 0,
      "startedAt": "2026-01-02T15:04:05Z",
      "status": "string"
    }
  ],
  "output": null,
  "parentExecutionId": "string",
  "resumeAt": "2026-01-02T15:04:05Z",
  "resumeUrl": "https://flows.example",
  "startedAt": "2026-01-02T15:04:05Z",
  "status": "string",
  "trigger": "string",
  "triggerNodeId": "string",
  "workflowId": "string",
  "workflowVersionId": "string"
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/executions/{id}/retry" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:read` — ownership is checked in the handler, which alone can know which workflow an execution belongs to.

## Evaluate an expression against an execution (`eval-expression`)

`POST /api/v1/executions/{id}/eval`

Evaluates one expression against the node outputs an execution's trace already stores, under the budget a node of that revision is given, and answers the value and its JSON shape. Nothing is written and nothing runs: this reads what a field held while the workflow ran. With a nodeId the expression is evaluated as that node's parameters were, against one item of one of its runs (run and item pick them), so $json is the item the node received. The grammar is the one every workflow document is already evaluated with, and $env is the runtime's allowlist rather than the process environment.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `id` | path | yes | string | Execution identifier |

Request body: `application/json` — `EvalExpressionInputBody` (required)

Request fields:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `expression` | yes | `string` | The expression to evaluate. A template carrying {{ }} delimiters is evaluated exactly as a document parameter is; without them the whole text is taken as the expression body. |
| `item` | no | `integer` | Which item of that run's input is $json, counted from 0 as $itemIndex counts them. Defaults to 0. Needs nodeId. |
| `nodeId` | no | `string` | The node whose parameters the expression stands in for: $json is an item the node received, $input its run's input, and every earlier node is read as it stood when that run started. Omit it to read the run through $node alone. |
| `run` | no | `integer` | Which run of the node, counted from 0 as $runIndex counts them: a delivery the runner pruned is not a run. Defaults to the last run that received items. Needs nodeId. |

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `EvalExpressionResource` |
| `default` | Error | `application/problem+json` |

Fields of the `EvalExpressionResource` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `type` | yes | `string` | The JSON shape the value has |
| `value` | yes |  | The evaluated result |

Request (minimal — required fields only):

```json
{
  "expression": "string"
}
```

Response (skeleton — every field, placeholder values):

```json
{
  "type": "string",
  "value": null
}
```

```sh
curl -X POST "$KILASFLOW_HOST/api/v1/executions/{id}/eval" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "content-type: application/json" \
  -d '{"expression":"string"}'
```

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

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/executions/{id}/events" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Allow with `workflow:read` — ownership is checked in the handler, which alone can know which workflow an execution belongs to.
