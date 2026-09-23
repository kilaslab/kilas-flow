---
title: Events
description: The server-sent event types an execution emits, and how to resume the stream.
sidebar:
  order: 11
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

`GET /api/v1/executions/{id}/events` is a server-sent event stream. The OpenAPI document registers the endpoint; the vocabulary carried over it is below. The names are stable — see [API contract and stability](/reference/api-contract/).

| Event | Closes the stream | Meaning |
| --- | --- | --- |
| `execution.started` | no | The run left the queue and started. |
| `execution.completed` | yes | The run finished successfully. |
| `execution.failed` | yes | The run finished with an error. |
| `execution.cancelled` | yes | The run was cancelled. |
| `node.started` | no | A node started. |
| `node.output` | no | A node emitted an intermediate output. |
| `node.completed` | no | A node finished successfully. |
| `node.failed` | no | A node finished with an error. |
| `execution.waiting` | no | The run suspended at a Wait and is parked until it resumes. |
| `workflow.saved` | no | The workflow document changed under a running execution. |
| `webhook.response` | no | A Respond to Webhook node answered the caller. |
| `ai.model.started` | no | An AI node sent one request to its model. `data` names the model and the tool-loop iteration. |
| `ai.model.delta` | no | Streamed model output. `data.delta` is the next chunk of text, coalesced to about four updates per second. |
| `ai.model.completed` | no | One model request finished. `data.usage` carries token counts when the provider reports them. |
| `ai.tool.started` | no | An agent called a tool. `data.tool` names it and `data.detail` carries the redacted arguments. |
| `ai.tool.completed` | no | A tool call returned. `data.detail` carries the redacted result. |
| `ai.tool.failed` | no | A tool call failed. `data.error` says why; the agent sees the error and may retry. |
| `ai.agent.completed` | no | An agent produced its final answer. |
| `ai.agent.failed` | no | An agent run ended with an error. |
| `execution.event` | no | Any event type added after this client was built. The payload's `type` carries its real name; ignore what you do not recognise. |

Every event shares one shape, as served:

| Field | Type | Description |
| --- | --- | --- |
| `at` | string |  |
| `data` |  | Redacted, type-specific detail |
| `executionId` | string |  |
| `id` | integer | Monotonic per execution; send back as Last-Event-ID to resume |
| `nodeId` | string |  |
| `sequence` | integer |  |
| `status` | string |  |
| `type` | string |  |
| `workflowId` | string |  |

Delivery promises: each event carries a monotonic numeric `id`; a reconnecting client resends it as `Last-Event-ID` (or `?from=` where a header cannot be set) and resumes instead of restarting — retained events replay from that point. A comment heartbeat arrives every twenty seconds to keep idle connections open. The server closes the stream after a terminal event (`execution.completed`, `execution.failed`, `execution.cancelled`), so a client must stop reconnecting then. Event `data` is redacted on publish — a subscriber can never see credential material even if a node returned it. Delivery is best effort: durable execution and node-run records are the source of truth, and a run succeeds whether or not anyone is watching. New event types may be added in a minor release; consumers must ignore names they do not recognise.
