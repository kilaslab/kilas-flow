---
name: kilasflow-error-handling
description: Use when a run failed, when a failure must be caught, retried, alerted on or answered to an HTTP caller, or when a graph stops on purpose. Triggers on "error", "failed", "retry", "timeout", "error workflow", "respond".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow run
  - kilasflow exec get
  - kilasflow exec trace
  - kilasflow workflow get
  - kilasflow api
kilasflow_operations:
  - run-workflow
  - get-execution
  - stream-execution-events
  - get-workflow
  - update-workflow
  - activate-workflow
kilasflow_nodes:
  - kilasflow.errorTrigger
  - kilasflow.stopAndError
  - kilasflow.respondToWebhook
kilasflow_expression_roots:
  - $json
kilasflow_not_shipped:
  - 'No single-node retry verb: exec retry is not shipped, so a failed node is retried by running the workflow again'
---

## Non-negotiables

1. Read the failure before changing anything. `kilasflow exec get <executionId>` (`get-execution`) is the record and its node runs; `kilasflow exec trace <executionId>` (`stream-execution-events`) is the event feed, ending at the terminal event. A caller's 500, or a status of `failed`, is not a diagnosis.
2. Never change a document to fix a run without reading it first. `kilasflow workflow get <workflowId>` (`get-workflow`) is what carries the node `settings` and the workflow `settings` an error workflow lives in (internal/workflow/document.go, `Node.Settings` and `Document.Settings`); write it back with `kilasflow api update-workflow --path id=<workflowId> --body @wf.json` (`update-workflow`).
3. An error workflow that is not active is never started. The failure path starts the target as a child execution, and a child needs an active revision (`internal/repository/executions.go`, `StartChild`); activate the error workflow itself with `kilasflow api activate-workflow --path id=<workflowId>` (`activate-workflow`) before relying on it.
4. Never re-run a failed run to "retry the node" until you know the run is safe to repeat. There is no single-node retry (see Not shipped yet): a second run performs every side effect a second time.

## Strong defaults

- Manual runs are asynchronous. `kilasflow run <workflowId>` (`run-workflow`) answers 202 with the execution id and never the result; `--wait` polls it to a terminal status, `--input`/`--input-file` supply the manual input, `--trigger` picks the root when a workflow has several (internal/cli/verbs_run.go). A run that ends `failed` makes the verb exit non-zero with the error code `execution_failed`, and the record travels in the error detail.
- Every failure is a code, not a sentence. The record's `error` is `{code, message}`, with the code `execution.failed`, `execution.timeout` when a node reported the deadline, or `execution.cancelled` for a cancellation; each failed node run carries its own structured error, whose code names the cause — `node.failed`, `node.timeout`, `node.partial`, `config.invalid` or `execution.cancelled` (internal/engine/service.go, `structuredError` and `traceRows`; internal/engine/runner.go).
- A trace tells a retry from a loop. Every node attempt is its own row with an incrementing `attempt`, and every distinct run of a node inside a loop carries `runIndex`; `sequence` orders the trace (internal/api/handlers/workflows.go, `ExecutionNodeRunResource`). The human rendering of `exec get` is a summary, so read the node runs with `kilasflow exec get <executionId> --json`.
- Retry a flaky node, not a flaky workflow. A node's own `settings` carry `retryOnFail`, `maxTries` (default 3) and `waitBetweenTries` (default 1000 ms); with no `waitBetweenTries` the non-zero default is what stops one failing request becoming a burst (nodes/core.go, `sharedSettings`; docs/src/content/docs/concepts/execution-model.md).
- The same settings are bounded at compile time, not at run time: `maxTries` 1 to 8 (`workflow.MaxRetryAttempts`), `waitBetweenTries` 0 to 300000 ms (`workflow.MaxRetryWaitMilliseconds`) and `timeoutSeconds` 0 to 86400 are refused before activation (internal/workflow/compiler.go, `validateSharedSettings`).
- The per-node deadline is `timeoutSeconds`; the per-run budget is the workflow's own `settings.executionTimeout` in seconds, where `-1` means no run timeout, capped by the instance's `execution.max_timeout` (internal/engine/service.go, the run budget; docs/src/content/docs/concepts/execution-model.md).
- A node's `settings.onError` decides what a failure costs: `stopWorkflow` (the default) fails the run, `continueRegularOutput` emits an `error` item per failed item on the main output, and `continueErrorOutput` routes those items to the node's own extra `error` port, which the compiler declares for exactly that mode (internal/workflow/compiler.go, `withErrorPort`; internal/engine/runner.go, `ErrorItemKey`). The legacy `continueOnFail` is the same as `continueRegularOutput`.
- An error workflow is a separate workflow, named by id in the failing workflow's `settings.errorWorkflow`, and it starts from `kilasflow.errorTrigger` — a root with no inputs that passes the error object through (internal/engine/service.go, `errorWorkflowID` and `errorWorkflowItem`; nodes/error_workflow.go). Read it with `{{ $json.execution.error.message }}`, `$json.execution.id`, `$json.execution.lastNodeExecuted`, `$json.workflow.name` or `$json.trigger.mode`.
- The error run is best effort and narrow: it starts only for a `failed` run — never for a cancellation — only after the failed run is terminal, as its own execution parented to the failure, and a workflow naming itself is refused rather than recursed (internal/engine/service.go, `runErrorWorkflow`).
- A graph can declare its own failure with `kilasflow.stopAndError`, whose message is resolved per item and whose error object (n8n's JSON text shape) supplies the message when the parameter is empty; the run then fails exactly as a node that failed on its own, error workflow included (nodes/error_workflow.go, `executeStopAndError` and `stopAndErrorMessage`).
- A webhook answers by its own mode: `immediate` acknowledges before the graph runs, `lastNode` replies with the last node's items, and `responseNode` replies with what a `kilasflow.respondToWebhook` node produced (nodes/webhook.go, the `responseMode` options). A run that fails before any Respond node answers is answered with a generic 500 body, a run that outlives the response timeout with 504, and a Respond answer already persisted is preferred over both — a node that answered did answer (internal/webhook/webhook.go, `respondFromExecution`).

## Decision tree

```
what is in front of you?
|
+-- a run ended failed
|     -> kilasflow exec get <executionId> --json          (the record and its node runs)
|        then kilasflow exec trace <executionId>          (the events, ending at the outcome)
|        the first node run whose status is failed is where the run stopped
|
+-- one flaky node must survive a blip
|     -> on that node: settings.retryOnFail true, maxTries within 1..8,
|        waitBetweenTries within 0..300000 ms
|
+-- one failure must not end the run
|     -> on that node: settings.onError continueRegularOutput
|        or continueErrorOutput and wire the node's error port somewhere useful
|
+-- a failure must alert somebody
|     -> settings.errorWorkflow = <the alerting workflow's id> on the failing workflow
|        that workflow carries kilasflow.errorTrigger as its root and must be active
|
+-- the graph must say "this is an error, not an empty result"
|     -> a kilasflow.stopAndError node, with the message the run should fail with
|
+-- a webhook caller is waiting
|     -> trigger responseMode responseNode plus a kilasflow.respondToWebhook node
|        so a caller gets the answer the graph meant, not a generic failure body
|
+-- the failure is a timeout
      -> the node's timeoutSeconds, or the run's settings.executionTimeout
         (and the instance's execution.max_timeout ceiling above both)
```

## Not shipped yet

- No single-node retry verb: exec retry is not shipped, so a failed node is retried by running the workflow again — a partial run is finished by hand: fix the node's configuration in the document, then start a whole new run with kilasflow run and watch it to a terminal status. Confirm first that the graph's side effects are safe to repeat, because the second run performs all of them again, and treat the failed execution as history rather than something to resume.

## Anti-patterns

- "The run failed, so the workflow is broken" → usually one node's input, configuration or credential, not the graph → read the first failed node run in `kilasflow exec get <executionId> --json`, then the trace.
- "I'll just run it again" → with no single-node retry, a second run repeats every side effect of the first → fix the cause, then run once; for a genuinely flaky upstream set `retryOnFail` with a wait on the node instead.
- "Maximum attempts 20" → refused at compile time: `maxTries` is bounded at 8 → ask what the upstream can bear, and give the node a `waitBetweenTries` above zero.
- "Retry with no wait" → the default is 1000 ms for a reason: a burst against an upstream that is already failing turns one error into a queue → keep a non-zero wait, and let the retry budget stay small.
- "The error workflow will catch it" → one that is not active, or that names its own workflow, is never started, and the failure is best effort and logged (internal/engine/service.go) → activate it and test it by making a workflow fail on purpose.
- "The alerting workflow reads the node name" → the payload is n8n's shape, so the node is `$json.execution.error.node.name` and the message `$json.execution.error.message` → build the message from those keys, not from a renamed one.
- "Return the failure to the webhook caller" → the caller gets a generic body on purpose, because the URL may be public → read the failure in the execution record, and answer the caller with a `kilasflow.respondToWebhook` node where the graph can.
- "A cancelled run will alert" → cancellation is not a failure and starts no error workflow → treat a cancellation as an operator action, not as an incident.
- "The retried node succeeded" → the failed attempts are still rows → read `attempt` on the node's runs before concluding the node was fine all along.
