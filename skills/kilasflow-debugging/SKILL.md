---
name: kilasflow-debugging
description: Use when a workflow run failed, produced the wrong output, or never started, and when you need to find which node was at fault before changing anything. Triggers on "failed", "error", "wrong output", "debug", "trace".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow context
  - kilasflow run
  - kilasflow exec list
  - kilasflow exec get
  - kilasflow exec trace
  - kilasflow exec tail
  - kilasflow exec cancel
  - kilasflow workflow get
  - kilasflow workflow get-version
  - kilasflow workflow diagnostics
  - kilasflow api
kilasflow_operations:
  - list-executions
  - get-execution
  - stream-execution-events
  - cancel-execution
  - run-workflow
  - get-workflow
  - get-workflow-version
  - workflow-diagnostics
kilasflow_nodes:
  - kilasflow.errorTrigger
  - kilasflow.stopAndError
kilasflow_expression_roots:
  - $json
kilasflow_not_shipped:
  - 'No single-node retry verb. `exec retry` reruns the whole workflow with the same input and trigger, so a failed node is retried by running the workflow again'
---

## Non-negotiables

1. Read before patching. `kilasflow exec get <executionId>` (`get-execution`) is the only thing that says what ran: the status, the structured `error`, and every node run with its input, output and error. A fix written from the run's status alone is a guess about a graph nobody read.
2. Name the failing node before explaining it. On a failed execution, take the node run whose `status` is `failed` with the lowest `sequence`: that node's error ended the run, and everything after it never executed. Cross-check the execution's own status first — a tolerated failure leaves a `failed` row under a run that succeeded.
3. A refusal is not a failure. A 422 `workflow validation failed` means nothing was queued and the fix is in the document; a `failed` execution means the graph ran and stopped at a node. Debugging the second as though it were the first wastes a run.
4. The durable record is the authority; the live feed is best effort. The broker drops events for a subscriber that cannot keep up and keeps only a bounded per-execution history, so an event you did not see is not evidence that a node did not run.

## Strong defaults

- Start from the validated example: `kilasflow skills show kilasflow-debugging --reference EXAMPLE.md` is a small document with a Code node and the order to read its run in: `exec get`, `exec trace`, then `debug eval`.
- Start with `kilasflow context`: server URL, readiness, health, identity, workflows, datastores and the node-type count in one read-only call, where a failed section says so under its own error instead of disappearing.
- Find the run with `kilasflow exec list` (`list-executions`): `--workflow`, `--status` (repeatable, for example `failed`), `--trigger` and `--limit`/`--cursor`. The listing is summaries only — no input, output, error or trace — so the record itself still comes from `kilasflow exec get <executionId>`.
- Read a node run in this order: `status` first, because a node nothing was delivered to is `skipped` rather than failed; then `error` with its code; then `input`; then `output`. `attempt` counts retries of one run and `runIndex` counts the Nth time a node ran, so a retry and a loop iteration are different rows by design.
- Every failed attempt is a row of its own. A node that succeeded on its third try leaves rows for attempts 1 and 2 carrying `node.failed`, `node.timeout`, `execution.timeout` or `config.invalid`, then the completion row.
- A tolerant node does not fail the run: `continueOnFail` emits error items, and its row carries `node.partial` — or `node.failed` when every input item failed — so the node that received those error items is the next thing to read.
- The execution's own error code says which class of failure it was: `execution.failed`, `execution.timeout` when a node reported hitting its deadline, `execution.cancelled`, `execution.crashed` when an expired lease had to be handed on twice, and `wait.expired` when a call-resumed wait was never resumed.
- Follow a run that is still going with `kilasflow exec trace <executionId>` (`stream-execution-events`), which collects the feed into one envelope and stops at the outcome. `terminal: false` with a `lastEventId` is a fact about the run rather than a failed call: resume with `--from <lastEventId>`, which is sent as `Last-Event-ID`. `kilasflow exec tail <executionId>` streams one object per line and keeps its deadlines in the exit code — 6 means the run had not finished.
- Debug the revision that ran, not the workflow as it is now. `kilasflow exec get` names `workflowVersionId`, and `kilasflow workflow get-version <workflowId> <versionId>` (`get-workflow-version`) returns that immutable document.
- Check which revision is live with `kilasflow workflow get <workflowId>` (`get-workflow`): a webhook or schedule delivery runs the `activeVersion`, while a manual `kilasflow run <workflowId>` (`run-workflow`) queues the latest saved revision. Re-running something you just changed is not re-running what the caller hit.
- Prove an expression with a run rather than by reasoning about it: the item stream a node received is its `input` in the record and `$json` is one item of it, and an unresolvable reference fails the node with a message naming what it could not find — `$('Name') names a node that has not produced output in this run`.
- `kilasflow workflow diagnostics <workflowId>` (`workflow-diagnostics`, `--version-id` for an older revision) reads the import report stored with a revision. An empty `source` means the revision was never imported, which is a different fact from an import that had nothing to report. Issues are `blocking`, `lossy` or `dropped`; see the kilasflow-import-export skill.
- Alert instead of watching by hand: `settings.errorWorkflow` names a workflow ID, and the engine starts it from `kilasflow.errorTrigger` with an item carrying the failed execution's id, the mode, the last node that errored and the message — after the run is terminal, and never for a cancellation. `kilasflow.stopAndError` is how a graph says "this is an error, not an empty result".
- Stop a run nobody wants with `kilasflow exec cancel <executionId>` (`cancel-execution`). It answers 202 because the request was accepted, not because the run stopped, so read the execution back.
- Anything with no verb is one call away: `kilasflow api <operation-id>`.

## Decision tree

```
something is wrong
|
+-- a save, an activation or a run request was refused with 422
|     -> nothing executed: that is a compile refusal, not a run failure
|        each issue names a code and the node or connection it is about
|
+-- a run failed
|     -> kilasflow exec get <executionId>
|        the failed nodeRuns[] entry with the lowest sequence is the cause
|        read its error, then compare its input with what the node and its
|        parameters were told to expect
|        an errorCode of config.invalid on the row means the node's own settings
|        could not be resolved before it ran
|
+-- the run succeeded and the answer is wrong
|     -> take the last node's output and walk sequences back
|        the first node whose output is not what you meant is the defect
|
+-- nothing is in the history at all
|     -> kilasflow context, then kilasflow exec list --workflow <id>
|        a caller that never reached a trigger is a trigger problem
|        (the triggers skill), not an execution problem
|
+-- the status is waiting
|     -> the record carries resumeUrl and approvalUrl only while it waits;
|        a parked run holding no worker is not a failed run
|
+-- the record has disappeared
|     -> execution.retention prunes finished runs and is off by default;
|        deleting a tenant deletes its executions and their payload files
|
+-- it is still running, or you cannot tell
      -> kilasflow exec tail <executionId>, whose exit code 6 means "not finished"
         or kilasflow exec trace --from <lastEventId>
```

`kilasflow debug eval --execution <executionId> --node <nodeId> [--run <n>] [--item <n>] '<expression>'` evaluates an expression against a finished run's stored trace, as that node's parameters saw it — the item it received, its run index and the nodes before it — so a reference can be checked without another run.

## Not shipped yet

- No single-node retry verb. `exec retry` reruns the whole workflow with the same input and trigger, so a failed node is retried by running the workflow again. A re-run is a fresh execution and repeats every side effect its earlier node runs performed, so re-run the whole workflow only once you have read the trace, and prefer a node's own `retryOnFail` settings when the upstream is merely flaky.

## Anti-patterns

- "The run failed, so the workflow is broken" → usually one node's input, credential or expression is wrong, and the graph is fine → `exec get` the record, read the lowest-sequence failed node, and fix that node.
- "The status is failed, so everything failed" → one untolerated failure ends the run, and nodes after it are absent rather than failed → read which node errored and which runs never happened; the untaken arm is recorded as `skipped`.
- "The node succeeded, so its retry never happened" → a failed attempt is a row of its own, so a node that succeeded on the third try has rows for attempts 1 and 2 → read `attempt`, not just the final status.
- "The event never arrived, so the node never ran" → the feed is best effort and drops events for a slow subscriber → read the durable record; the trace rows are the source of truth.
- "The output field is `[redacted]`, so that is the bug" → redaction withholds credential-shaped values on the way out, and the trace already redacted on the way in → the cause is elsewhere; the marker means withheld, not missing.
- "It fails in production but not in my manual run" → a manual run queues the latest saved revision while a delivery runs the active one → compare `workflowVersionId` on the failing execution with `activeVersion` from `workflow get` before changing anything.
- "I re-ran it and it failed again, so it is deterministic" → a worker that died mid-run leaves an execution another worker reclaims and re-runs from the beginning, side effects included → check the execution's error code before trusting the second failure: `execution.crashed` is that path, not a reproducible defect.
- "I'll cancel and start over" → 202 means the cancellation was accepted, and a queued run is cancelled without ever being claimed while a waiting one is completed at once → read the execution back before assuming it stopped.
- "It must be the import, because the graph is nonsense" → the import report is stored per revision and is empty for a workflow never imported → `workflow diagnostics`, and read `source` before blaming the importer.

## Reference files

| File | Read when |
| --- | --- |
| EXAMPLE.md | you want a small document to run and then read back, with the commands to read it in the order that finds the fault |
| TRACE_READING.md | you have an execution id and need to read a node run, the event vocabulary or the difference between a validation refusal and a run failure without guessing |
