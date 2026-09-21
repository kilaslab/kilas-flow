# Reading a trace

`kilasflow exec get <executionId>` (`get-execution`) returns one execution and
its node runs. This file is what each field means, which of them answer "what
broke", and how a run failure differs from a refusal that stopped nothing.

## The execution record

| Field | What it tells you |
| --- | --- |
| `status` | where the run is: `queued`, `running`, `cancelling`, `waiting`, `succeeded`, `failed`, `cancelled` (internal/execution/records.go) |
| `trigger` | how it started: `manual`, `webhook`, `schedule`, `subworkflow` |
| `triggerNodeID` | which root it started from, when the document declares several |
| `parentExecutionID` | the execution that called this one, for a sub-workflow run |
| `workflowVersionID` | the immutable revision this run is pinned to; the document that actually ran |
| `input` | the item the run started from — the trigger's payload, not the caller's request |
| `output` | what the run produced, keyed as the engine recorded it |
| `error` | a structured `{code, message}` object, present on a failed run |
| `nodeRuns[]` | one entry per node invocation, in execution order |
| `resumeUrl` / `approvalUrl` | present only while `status` is `waiting`; the machine link and the human page that end the suspension |

Redaction runs on the way out (and on the way into storage for node runs), so a
credential-shaped value appears as `[redacted]`: a visible marker rather than an
omission, so a reader can tell "withheld" from "never present"
(internal/execution/redact.go).

A `waiting` run holds no worker and no lease until a resume re-queues it, so it
is neither stuck nor failed (docs/src/content/docs/concepts/execution-model.md).

## The run's error code

The execution's `error.code` names the class of failure:

| Code | Meaning | Where it is set |
| --- | --- | --- |
| `execution.failed` | a node failed without tolerating it | internal/engine/service.go |
| `execution.timeout` | a node run reported hitting the execution deadline | internal/engine/runner.go, `invoke` |
| `execution.cancelled` | the context was cancelled | internal/engine/service.go |
| `execution.crashed` | an expired worker lease was handed on twice, so the run was settled instead of given to a third worker | internal/repository/executions.go |
| `wait.expired` | a call-resumed wait nobody resumed passed its deadline | internal/repository/waits.go, `WaitExpiredCode` |

A recovered execution re-runs from the beginning rather than resuming, because
the checkpoint belongs to a suspension and not to a crash — so side effects of
the abandoned attempt may happen twice, and the abandoned trace is deleted when
the lease is reclaimed (docs/src/content/docs/concepts/execution-model.md).

## One node run

| Field | What it tells you |
| --- | --- |
| `nodeId` | which node; resolve it against the revision's document |
| `attempt` | which try this row is, counting from 1. Every failed attempt is its own row, so a node that succeeded on the third try has three rows (internal/engine/runner.go, `invoke`) |
| `runIndex` | the Nth time this node ran in the execution, from zero — a loop body or a fan-out produces several, and this is deliberately separate from `attempt` |
| `sequence` | monotonic position in the run; on a failed execution the failed row with the lowest sequence is the cause |
| `status` | `succeeded`, `failed` or `skipped` — a node no incoming channel delivered to is `skipped`, which is not a failure (internal/engine/runner.go, `skip`). A row with an error is `failed` even when the node tolerated it, so check the execution's own status before calling a row the cause |
| `input` | the item stream the node was invoked with; this is what `$json` resolved against |
| `output` | what it emitted, per port |
| `error` | the node's own error, with an `errorCode` |
| `response` | the HTTP answer a `kilasflow.respondToWebhook` node produced, persisted so a boundary in another process answers from the record |

Node error codes worth recognising: `node.failed` (the executor returned an
error), `node.timeout` (the node's own timeout), `execution.timeout` (the run's
budget), `config.invalid` (the node's configuration could not be resolved before
it ran), `execution.cancelled`, and `node.partial` / `node.failed` from the
per-item path where `continueOnFail` turned failures into error items
(internal/engine/runner.go).

Two settings explain most surprising rows: `retryOnFail` with `maxTries` and
`waitBetweenTries` produce the extra attempt rows, and `continueOnFail` is why a
failed node did not fail the run — its error items are the input the next node
saw (docs/src/content/docs/concepts/execution-model.md).

## The live feed

`GET /api/v1/executions/{id}/events` is a server-sent event stream fed by an
in-process broker; `kilasflow exec trace` collects it and `kilasflow exec tail`
streams it (internal/events/events.go; internal/cli/verbs_exec.go).

| Event | Emitted? |
| --- | --- |
| `execution.started` | yes, when a worker claims and begins a run |
| `execution.completed` | yes, terminal |
| `execution.failed` | yes, terminal, carrying the redacted error detail |
| `execution.cancelled` | yes, terminal |
| `execution.waiting` | yes, deliberately non-terminal, so a feed stays open across a wait |
| `node.completed` | yes, after the node's row is durable — and for a skipped node too, since publishing a failure for a pruned branch would light up an arm that never ran |
| `node.failed` | yes, after the node's row is durable |
| `node.started`, `node.output`, `workflow.saved` | no — declared, typed and never published |
| `ai.model.*`, `ai.tool.*`, `ai.agent.*` | yes, from inside a long-running AI node; nested progress, not authoritative state |

Three consequences for a debugger:

- A node event is published only after that node's run is durable, so a
  subscriber can never observe a state the record does not already carry. The
  exceptions are the nested AI events, which carry no authoritative state.
- Events carry a monotonic per-execution id, so `--from <lastEventId>` (the
  `Last-Event-ID` header) resumes rather than restarting. Delivery is best
  effort: a subscriber that cannot keep up has events dropped rather than
  stalling the run, and a finished execution whose events have aged out has its
  terminal frame reconstructed from the record instead (internal/api/handlers/
  executions.go, `syntheticTerminal`).
- The stream closes at a terminal event, which is what lets a client stop
  reconnecting.

## A refusal is not a failure

Two different answers, at two different moments:

- **Refused before anything ran** — 422, nothing queued. `workflow draft is
  invalid` is a structural fault found on save; `workflow validation failed`
  carries the compiler's issues, each with a stable code (`node.unknown_type`,
  `config.required`, `port.full`, `workflow.invalid_topology`, …), a path and
  the node or connection it is about (internal/api/handlers/workflows.go,
  `draftProblem` and `compileProblem`; internal/workflow/compiler.go). No
  execution row exists, so there is no trace to read: the fix is the document.
- **Failed while running** — an execution row with `status: failed` and a
  `nodeRuns[]` entry that errored. The graph executed and stopped; the fix is
  usually the failing node's input, credential or configuration.

A run request refused with 422 because of `triggerNodeId` is the middle case
worth knowing: the node cannot start a run — it is disabled, it is not this
workflow's, or something feeds into it — and nothing was queued
(internal/repository/executions.go, `manualStartProblem`).

## A reading order that answers "why"

1. `status` and `error.code` — class of failure.
2. The failed node run with the lowest `sequence` — the cause.
3. Its `error` and `attempt` — the executor's own message, and whether earlier
   attempts failed the same way.
4. Its `input` — what the node was actually given.
5. The revision named by `workflowVersionID` — what the node was configured to do,
   through `kilasflow workflow get-version <workflowId> <versionId>`.
6. The nodes after it — they are absent from the run, not failed; a `skipped`
   row is the untaken arm of a branch.
