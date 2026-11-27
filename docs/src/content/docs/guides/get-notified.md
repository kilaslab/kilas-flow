---
title: Get notified of results
description: How your backend learns that a run finished or failed. KilasFlow does not push execution results to you yet. This page covers the patterns that work today, with runnable examples, and what is missing.
---

:::caution[There are no outbound execution webhooks]
KilasFlow does not call your backend when a run finishes or fails. The OpenAPI
document has no subscription or callback operation, there is no signed delivery,
no retry queue and no delivery log. A tenant-level notification feed is planned
as [FEAT-39ttf6](https://github.com/kilaslab/kilas-flow/blob/main/.pine/tickets/FEAT-39ttf6.md).
It depends on a durable outbox that does not exist yet,
[FEAT-wdxkvw](https://github.com/kilaslab/kilas-flow/blob/main/.pine/tickets/FEAT-wdxkvw.md).
Until then, build on the patterns below.
:::

Five things work today. Pick by what you need to know.

| You need | Use | Guarantee |
| --- | --- | --- |
| An alert when a customer's workflow fails | [An error workflow that calls your backend](#an-error-workflow-that-calls-your-backend) | One attempt, best effort |
| A call when a run succeeds | [A final HTTP Request node](#a-final-http-request-node) | Runs only if the workflow reaches it |
| A complete, durable record of what failed | [Poll the execution list](#poll-the-execution-list) | You pull, so nothing is missed |
| Live progress of one run | [The per-execution event stream](#the-per-execution-event-stream) | Best effort, one run per connection |
| UI updates in the page that mounted the editor | [The editor's postMessage events](#the-editors-postmessage-events) | Only that page, only its own runs |

For anything you cannot afford to miss, poll. The push patterns can lose a
notification, and nothing on the KilasFlow side records that they did.

## An error workflow that calls your backend

A workflow can name another workflow to run when it fails. KilasFlow starts that
workflow from an **Error Trigger** (`kilasflow.errorTrigger`) and hands it the
failure. The error workflow then calls your backend with an HTTP Request node.

### Name the error workflow

Set `settings.errorWorkflow` in the failing workflow's document to the **id** of
the error workflow:

```json
{
  "schemaVersion": 1,
  "name": "Customer automation",
  "settings": { "errorWorkflow": "wf_01a21bff-e4e4-7079-a3ad-9a2b70db6f7b" },
  "nodes": [],
  "connections": []
}
```

The editor has no field for this setting. Set it from your backend when you
create or update the workflow (`createWorkflow`, `updateWorkflow`), or let an
n8n import carry it in. It is an id, not a name, so renaming the error workflow
cannot silence it.

### Build the error workflow

This pair of nodes posts the failure to your backend. It was run against a real
server for this page and the request arrived with every field filled in:

```json
{
  "schemaVersion": 1,
  "name": "Tell the host about failures",
  "settings": {},
  "nodes": [
    {
      "id": "trigger",
      "name": "Error Trigger",
      "type": "kilasflow.errorTrigger",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 },
      "parameters": {}
    },
    {
      "id": "notify",
      "name": "Notify host",
      "type": "kilasflow.httpRequest",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "method": "POST",
        "url": "https://app.example.com/kilasflow/execution-failed",
        "sendBody": true,
        "bodyType": "json",
        "bodyFields": {
          "executionId": { "mode": "expression", "value": "{{ $json.execution.id }}" },
          "workflowId": { "mode": "expression", "value": "{{ $json.workflow.id }}" },
          "workflowName": { "mode": "expression", "value": "{{ $json.workflow.name }}" },
          "node": { "mode": "expression", "value": "{{ $json.execution.lastNodeExecuted }}" },
          "message": { "mode": "expression", "value": "{{ $json.execution.error.message }}" }
        }
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "trigger", "port": "main" },
      "target": { "nodeId": "notify", "port": "main" }
    }
  ]
}
```

Your backend receives this body:

```json
{
  "executionId": "exec_01a21bff-e50f-7ebd-ba57-154e15c6238d",
  "workflowId": "wf_01a21bff-e4fd-7d9c-802d-113ec92b0a7e",
  "workflowName": "Customer automation",
  "node": "Fail on purpose",
  "message": "execute node \"Fail on purpose\": the CRM refused the contact"
}
```

The Error Trigger's item has this shape, which is n8n's, so an error workflow
imported from n8n reads the same fields:

| Field | Value |
| --- | --- |
| `execution.id` | the failed run's id |
| `execution.url` | a link to the run's page, with `server.public_url` in front when it is set and a bare path when it is not |
| `execution.mode` | how the failed run started: `manual`, `webhook`, `schedule`, `hostEvent` and so on |
| `execution.lastNodeExecuted` | the name of the node that failed |
| `execution.error` | `message`, `stack`, `code`, `timestamp`, and `node` (`id`, `name`, `type`) when a node failed |
| `workflow.id`, `workflow.name` | the workflow that failed |
| `trigger.mode` | the same value as `execution.mode` |

Authenticate the call to your backend with a credential. Attach an
`httpHeaderAuth` or `httpBearerAuth` credential to the HTTP Request node, the
same way the embedding guide attaches one to a trigger
([credentials](/concepts/credentials/)), and scope it to your host name with
`allowedDomains`.

### What to know before relying on it

- **It is a separate workflow for each tenant.** A workflow can only name an
  error workflow in its own tenant. An id from another tenant, or one that was
  deleted, is not found, and the failed run's `error` says so
  (`"errorWorkflowError": "repository record not found: workflow"`). Create the
  error workflow in each customer's tenant when you provision it, with that
  tenant's own secret.
- **It runs the latest saved revision of the error workflow,** active or not. An
  error workflow you saved and never activated still runs.
- **It starts after the failed run is final,** so the error workflow reads a
  settled record. It starts for a run that fails, whatever started it: a
  webhook, a schedule, a host event or a manual run.
- **It does not start for a cancelled run.** A person stopping a run is not
  reported as a failure.
- **It does not start for a failure that is settled outside a worker.** A run
  whose worker died and used up its reclaim attempts is marked failed by the
  queue and never reaches the code that starts error workflows. A wait that is
  never resumed and fails at its deadline is settled by a sweeper, and does not
  start one either. Poll as the backstop.
- **It is best effort and does not retry.** If the error workflow cannot start,
  or its HTTP call fails, the failed run stays failed and the server logs
  `error workflow failed`. The failed run's `error` object records what
  happened: `errorWorkflowId` and `errorExecutionId` when it started, or
  `errorWorkflowError` when it did not.
- **A workflow cannot name itself.** That would loop, so it is refused and noted
  on the run.
- **Egress is governed.** The HTTP Request node cannot reach a loopback or
  private address by default, so a host on `10.x`, `192.168.x`, `172.16.x` or
  `localhost` is refused with `request target is not allowed`. The operator
  admits one host and port with `outbound.allowed_private_endpoints`
  (`KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS=127.0.0.1:19090`). See
  [Safety boundaries](/concepts/safety-boundaries/).
- **The setting is part of the document a user saves.** A session with
  `workflow:write` saves the whole document, so do not rely on `errorWorkflow`
  as a control. It is a notification.

## A final HTTP Request node

To hear about success, end the workflow with an HTTP Request node that posts the
result to your backend. It is the same node as above, connected to the last node
of the workflow instead of an Error Trigger.

Two settings make it more reliable. In the node's `settings`, `retryOnFail`
with `maxTries` (up to 8) and `waitBetweenTries` (milliseconds, up to 300000)
retries the call when your backend answers with an error. And `neverError` on
the node's parameters returns a 4xx or 5xx answer as an item instead of failing
the run, if you do not want a notification failure to fail a good run.

The limit is the one every push pattern has. If the run fails or is cancelled
before it reaches this node, nothing is sent. Pair it with an error workflow for
failures.

## Poll the execution list

`GET /api/v1/executions` is the durable source. Every run is recorded, and you
can filter by status, workflow and trigger.

```sh
curl "$KILASFLOW_URL/api/v1/executions?status=failed&limit=100" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

The statuses a run ends in are `succeeded`, `failed` and `cancelled`. Repeat
`status` to ask for more than one (`status=failed&status=cancelled`). The list is
newest first. It has no "since" filter, so remember the last run you handled
and read pages until you reach it. `limit` is capped at 100, and the page body
carries `nextCursor` for the next page ([Pagination](/reference/api/pagination/)).

A list entry does not carry the error or the output. Read the run with
`GET /api/v1/executions/{id}` when you need them. This function finds new
failures and reads each one once. It was run against a real server for this
page:

```js
const { KILASFLOW_URL, KILASFLOW_API_KEY } = process.env;

async function api(path) {
  const response = await fetch(`${KILASFLOW_URL}/api/v1${path}`, {
    headers: { Authorization: `Bearer ${KILASFLOW_API_KEY}` }
  });
  if (!response.ok) throw new Error(`${path} answered ${response.status}`);
  return response.json();
}

export async function newFailures(seen) {
  const found = [];
  let cursor = '';
  for (;;) {
    const page = await api(`/executions?status=failed&limit=100${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`);
    for (const summary of page.items ?? []) {
      if (seen.has(summary.id)) return found;
      // The listing omits the error. One read per new failure gets it.
      const execution = await api(`/executions/${summary.id}`);
      found.push({ id: execution.id, workflowId: execution.workflowId, error: execution.error });
    }
    if (!page.nextCursor) return found;
    cursor = page.nextCursor;
  }
}
```

Call it on a timer, add each returned id to `seen`, and persist `seen` so a
restart does not alert twice. With the SDK, `client.iterateExecutions({ status:
['failed'] })` does the paging.

To see only one customer's runs, use that customer's tenant key. A tenant key
cannot see another tenant's runs. Runs are kept until `execution.retention`
removes them, which is off by default, so poll more often than your retention
window.

## The per-execution event stream

`GET /api/v1/executions/{id}/events` is a server-sent event stream for one run.
It replays the events retained so far, streams new ones, and closes after the
terminal event (`execution.completed`, `execution.failed` or
`execution.cancelled`). The vocabulary is on [Events](/reference/api/events/).

From a backend you send the API key as a normal header. This function waits for
a run to finish. It was run against a real server for this page:

```js
const TERMINAL = new Set(['execution.completed', 'execution.failed', 'execution.cancelled']);

async function waitForTerminalEvent(baseUrl, apiKey, executionId) {
  const response = await fetch(`${baseUrl}/api/v1/executions/${executionId}/events`, {
    headers: { Authorization: `Bearer ${apiKey}`, Accept: 'text/event-stream' }
  });
  if (!response.ok) throw new Error(`the event stream answered ${response.status}`);

  const decoder = new TextDecoder();
  let buffer = '';
  for await (const chunk of response.body) {
    buffer += decoder.decode(chunk, { stream: true });
    let end;
    while ((end = buffer.indexOf('\n\n')) !== -1) {
      const frame = buffer.slice(0, end);
      buffer = buffer.slice(end + 2);
      const line = frame.split('\n').find((candidate) => candidate.startsWith('data: '));
      if (!line) continue; // a heartbeat comment
      const event = JSON.parse(line.slice('data: '.length));
      if (TERMINAL.has(event.type)) return event;
    }
  }
  throw new Error('the stream closed before a terminal event');
}
```

The terminal `execution.failed` event carries `data.code` and `data.message`.
From a browser, `EventSource` cannot send a header, so your backend mints a
single-use ticket for the run and the page spends it as `?ticket=`. The SDK's
`subscribeExecutionEvents` does this and resumes after a drop
([Using the host SDK](/guides/sdk/#watching-a-run-stream-tickets)).

What to know:

- It needs the run id first. Get it from the run API's `202`, from a webhook that
  answers with `$execution.id` ([Trigger workflows from your app](/guides/trigger-workflows/#what-the-caller-gets-back)),
  or from the list.
- **It is best effort.** The broker that feeds it lives inside one server
  process and keeps a bounded history per run. A run that finished before you
  connected replays its outcome and closes, and a run whose record is gone
  answers `404`. In a deployment that splits API and worker processes, an event
  raised on one process does not reach a subscriber on another. Keep polling as
  the source of truth.
- **It costs a connection per run.** That is fine for a user watching one run.
  It does not scale to "tell me about every run for every tenant".
- A run that is settled as crashed is never published to the stream.

## The editor's postMessage events

An embedded editor posts `execution-started` and `execution-finished
{ executionId, status }` to the page that mounted it, with `workflow-saved`,
`workflow-published` and the rest ([the protocol](/guides/embedding/#the-postmessage-protocol-v2)).
They are for your UI. They reach only that browser page. They cover the runs
the user started in that editor, not runs started by a webhook, a schedule or a
host event, and they vanish when the page closes. Use them to refresh a status
badge. Do not treat them as a record.

## For whoever runs the server

If you operate KilasFlow yourself, two things give you a tenant-wide failure feed
without any workflow.

Every run that reaches a terminal state writes one structured log line, `execution
finished`, at `WARN` for a failed run and `INFO` otherwise:

```text
level=WARN msg="execution finished" status=failed execution=exec_01a2… workflow=wf_01a2… workflowName="Customer automation" tenant=default trigger=manual durationMs=2 node=boom errorCode=execution.failed error="…"
```

The line carries identifiers, the failing node and the error text, and nothing
the run processed. `log.execution_level` (`KILASFLOW_LOG_EXECUTION_LEVEL`) sets
it to `info` (every run), `warn` (failed runs only) or `off`. A log shipper that
alerts on `level=WARN msg="execution finished"` gives you every failed run in
every tenant, with the `tenant` field to route it. It also records runs that the
error workflow and the stream miss.

The Prometheus endpoint counts the same outcomes as
`kilasflow_executions_finished_total{trigger,status}`
([Metrics](/operate/metrics/)).

Neither of these is an API your customers can call. They are for the operator.

## What is missing

The product has no way to say "tell this URL whenever any run in my tenant
fails". The planned feature is
[FEAT-39ttf6](https://github.com/kilaslab/kilas-flow/blob/main/.pine/tickets/FEAT-39ttf6.md):
one signed endpoint per tenant that receives `execution.failed`, and optionally
`execution.completed`, `workflow.activated` and `workflow.deactivated`, with
retries and a delivery log. Only `execution.completed`, `execution.failed` and
`execution.cancelled` exist as events today, and only on the in-process broker.
There is no activation event.

It cannot be built on that broker, because the broker is best effort and per
process. It needs a transactional outbox written in the same transaction as the
state change, and a dispatcher that claims rows, retries with backoff and signs
each delivery. That work is
[FEAT-wdxkvw](https://github.com/kilaslab/kilas-flow/blob/main/.pine/tickets/FEAT-wdxkvw.md).
Neither is started.
