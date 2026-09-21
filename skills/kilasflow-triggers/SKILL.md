---
name: kilasflow-triggers
description: Use when a workflow has to be started by something other than you — an inbound HTTP request, a hosted form, a cron schedule, a manual run or another workflow — or when an activated workflow receives nothing. Triggers on "webhook", "trigger", "schedule", "form", "activation".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow run
  - kilasflow schedule list
  - kilasflow workflow get
  - kilasflow workflow publish-events
  - kilasflow exec list
  - kilasflow exec get
  - kilasflow api
kilasflow_operations:
  - run-workflow
  - list-schedules
  - get-workflow
  - list-workflow-publish-events
  - list-executions
  - get-execution
  - list-workflow-webhooks
  - activate-workflow
  - deactivate-workflow
kilasflow_nodes:
  - kilasflow.manual
  - kilasflow.webhook
  - kilasflow.formTrigger
  - kilasflow.schedule
  - kilasflow.executeWorkflowTrigger
  - kilasflow.executeWorkflow
  - kilasflow.respondToWebhook
  - kilasflow.errorTrigger
kilasflow_expression_roots: []
kilasflow_not_shipped:
  - 'No activation verb: activation and deactivation are the activate-workflow and deactivate-workflow operations through the escape hatch'
  - 'No webhook discovery verb: the list-workflow-webhooks operation is served, but no verb lists the webhook URLs of a workflow yet'
  - 'No idempotency flag: the server honours Idempotency-Key on a run and on a row write, but no verb carries the key yet'
  - 'No guarded write verbs: credential create, update and delete, datastore create, rename, delete, columns and clear, schedule create, update and delete, pack install, and tenant create and delete have no verbs yet'
---

## Non-negotiables

1. Never activate to test. Activation mints and binds the workflow's public webhook routes, creates its schedule rows, compiles the latest revision and writes a publish audit row, all in one transaction (internal/repository/workflows.go, `publish`). Run it with `kilasflow run <workflowId>` (`run-workflow`) instead, and activate only when the user asked for the endpoint.
2. A delivery runs the **active** revision; a manual run runs the **latest saved** one. `POST /workflows/{id}/run` queues the newest revision without activating anything (internal/api/handlers/workflows.go, `Run` over `QueueManualLatest`), while a webhook, a schedule and a sub-workflow call all queue against the revision that was activated (internal/repository/executions.go, `QueueTriggered`). "It saves but the webhook still runs the old graph" is not a bug: activate again.
3. Never guess an inbound URL. A route is 16 random bytes hex-encoded to 32 characters, minted per trigger node and reused forever, so the `path` parameter is a label rather than an address — it is deliberately not unique, and the node's own description says two workflows in different tenants may share one (internal/webhook/webhook.go, `Extract`; internal/repository/webhooks.go, `isRouteCollision`; nodes/webhook.go, the `path` parameter's description). Read the real address with the `list-workflow-webhooks` operation through `kilasflow api`.
4. A trigger that authenticates but has nothing to check against is refused at activation, and once active it refuses every caller. The four inbound modes are `none`, `basicAuth`, `headerAuth` and `jwtAuth`, each needing a credential of the matching type attached to the node (nodes/webhook.go, `credentialTypesForAuth` and `validateWebhookConfiguration`). Leave it `none` only for an endpoint that is meant to be public, and know that a deployment can refuse those outright with `webhook.require_auth`.

## Strong defaults

- Five kinds start a run, and all five write the same queued execution row through the same durable queue (docs/src/content/docs/concepts/execution-model.md):
  - `kilasflow.manual` — the editor or a manual run; the root a bare `kilasflow run` starts from.
  - `kilasflow.webhook` — an inbound HTTP request on one of the methods the node declares (`httpMethod`, or `multipleMethods` with `httpMethods`).
  - `kilasflow.formTrigger` — a hosted page at the workflow's own URL that is a GET, plus the POST its submission is.
  - `kilasflow.schedule` — cron, from the node's Trigger Rules intervals or the legacy `cron` parameter.
  - `kilasflow.executeWorkflowTrigger` — where a workflow called by `kilasflow.executeWorkflow` begins.
- A trigger binds an endpoint only when it is enabled and resolves a non-empty path: extraction skips a disabled node, so switching one off is how a document stops serving a URL without deactivating the whole workflow (internal/webhook/webhook.go, `Extract`).
- The kind is recorded on the run: `manual`, `webhook`, `schedule` or `subworkflow` (internal/execution/records.go). A sub-workflow call, including an error workflow, is `subworkflow` and carries `parentExecutionId`.
- Check the state before believing anything: `kilasflow workflow get <workflowId>` (`get-workflow`) reports `active`, `latestVersion` and `activeVersion`. An inactive workflow has no endpoint and no schedule row, and every delivery to it is a 404.
- Start one yourself with `kilasflow run <workflowId>` (`run-workflow`): `--input` or `--input-file` for the item, `--trigger <nodeId>` to start from one trigger only, `--wait` to follow it and exit non-zero on a failed run (`--poll` sets the read-back interval; the global `--timeout` is the wait deadline, five minutes when you name none). A workflow that declares several triggers otherwise fires all of them with the same item (internal/cli/verbs_run.go).
- A trigger node it cannot start from is refused, not ignored: the answer is 422 with the node named, and `/triggerNodeId` as the path — a disabled node, a node this workflow does not have, or a node something feeds into (internal/repository/executions.go, `manualStartProblem`).
- Schedules are rows that activation keeps in step with the document. Read them with `kilasflow schedule list` (`list-schedules`, `--limit` and `--cursor`) and look at `cron`, `active` and `nextRunAt` (internal/api/handlers/schedules.go; internal/repository/workflows.go, `syncSchedules`). A schedule finer than the 15-second tick fires once per tick, not more often (internal/scheduler/scheduler.go, `TickResolution`).
- The publish trail is a read: `kilasflow workflow publish-events <workflowId>` (`list-workflow-publish-events`) records `published`, `unpublished` and `restored` with the actor and the reason (internal/api/handlers/workflows.go, `WorkflowPublishEventResource`). Activation writes one; deactivation writes the version that stopped serving.
- Confirm a delivery became a run with `kilasflow exec list --workflow <workflowId> --trigger webhook` (`list-executions`, `--status` is repeatable) and read the record with `kilasflow exec get <executionId>` (`get-execution`).
- Anything without a verb is one call away: `kilasflow api activate-workflow --path id=<workflowId>` (`activate-workflow`) turns a workflow on, and `kilasflow api deactivate-workflow --path id=<workflowId>` (`deactivate-workflow`, idempotent, retains the pinned revision) turns it off. Activation's answer carries `notices[]`, one per trigger that still needs you to paste a URL somewhere (internal/api/handlers/workflows.go, `ActivationResource`).
- A called workflow must be **active**: a sub-workflow execution is created already claimed and pinned to the called workflow's active revision, so a call to one that is deactivated fails the calling node (internal/repository/executions.go, `StartChild`; nodes/subworkflow.go). Activation refuses that document up front and names the node — "which is not active: activate that workflow first, then activate this one" — and reports a missing target differently, because the two need different fixes (internal/repository/workflows.go, `refuseInactiveSubworkflows`). A child run is its own execution row with `parentExecutionId`.
- A failure can be wired rather than watched: `settings.errorWorkflow` names another workflow's ID, and the engine starts it from `kilasflow.errorTrigger` with the error, after the failed run is terminal and never on a cancellation (internal/engine/service.go, `runErrorWorkflow`; nodes/error_workflow.go).
- Answer the caller from inside the run with `kilasflow.respondToWebhook`: the node publishes its status, headers and body as an event, so the caller is answered at that moment while the rest of the graph keeps going (nodes/webhook.go, `executeRespond`).

## Decision tree

```
a workflow did not start
|
+-- is it active?
|     -> kilasflow workflow get <workflowId>: active=false means no route and no schedule row
|        kilasflow api activate-workflow --path id=<workflowId>
|        a 409 here is a binding conflict: the same method bound twice on one route,
|        which an import declaring a single method beside a method list can produce
|
+-- active, and the caller got 404
|     -> read the minted address: kilasflow api list-workflow-webhooks --path id=<workflowId>
|        an unknown route, an inactive workflow and a wrong method are all the same 404
|        on purpose (internal/webhook/webhook.go, resolveBinding)
|
+-- the caller got 403
|     -> the deployment sets webhook.require_auth and this trigger authenticates nothing
|        set Authentication to Basic, Header or JWT auth, attach the credential, activate again
|
+-- the caller got 401, or 500, or 413
|     -> 401: this trigger's own check refused the delivery
|        500: its credential cannot verify anything — the endpoint's fault, not the caller's
|        413: the body was larger than webhook.max_body_bytes (1 MiB by default)
|
+-- the caller got 200 with "Workflow was started" and no run exists
|     -> kilasflow exec list --workflow <workflowId> --trigger webhook
|        a filtered delivery, an ignored bot and a duplicate all answer 200 too
|
+-- the schedule never fired
|     -> kilasflow schedule list: is there a row, is it active, is nextRunAt what you meant?
|        the row exists only while the workflow is active; times are the workflow timezone
|
+-- the graph called another workflow and that call failed
|     -> the called workflow must be active; its run is a separate execution row
|        with parentExecutionId (list-executions on the called workflow id)
|
+-- it never ran at all and the save was refused
      -> a compile refusal, not a trigger problem: nothing was queued
```

## Not shipped yet

- No activation verb: activation and deactivation are the activate-workflow and deactivate-workflow operations through the escape hatch — no verb path exists for either, so reach them through `kilasflow api` with `--path id=<workflowId>`, and read the workflow back to confirm `active` and `activeVersion` moved.
- No webhook discovery verb: the list-workflow-webhooks operation is served, but no verb lists the webhook URLs of a workflow yet — ask the server through `kilasflow api list-workflow-webhooks --path id=<workflowId>`, which mints the route on first read, so an address can be handed to a sender before the workflow is ever activated.
- No idempotency flag: the server honours Idempotency-Key on a run and on a row write, but no verb carries the key yet — a retried `kilasflow run` is a second execution. Send the header through the escape hatch if the retry must be safe, and remember a webhook delivery is deduplicated separately, by the sender's delivery id.
- No guarded write verbs: credential create, update and delete, datastore create, rename, delete, columns and clear, schedule create, update and delete, pack install, and tenant create and delete have no verbs yet — reach each operation through `kilasflow api`, and expect the schedule rows a workflow's own trigger owns to be created and removed by activation rather than by hand.

## Anti-patterns

- "I'll activate it so I can send it a request" → activation is a publish: it mints the public route, binds it, compiles the revision and fires the schedules → run the graph with `kilasflow run <workflowId> --wait` and read `kilasflow exec get <executionId>`; activate when the endpoint is the point.
- "The URL is the path I configured" → the `path` is a label; the address is an opaque minted route → read it with the `list-workflow-webhooks` operation and hand the sender that.
- "The caller gets one 404, so the route is wrong" → every miss answers the same 404, including a wrong method and an inactive workflow → check `active` first, then the method, then the address.
- "The sender cannot send a header, so I'll leave authentication off" → a trigger whose mode is `none` is publicly callable, and a deployment with `webhook.require_auth` refuses it outright → set Basic, Header or JWT auth and attach the credential the mode names; a mode with no matching credential is refused at activation rather than at the caller's first delivery.
- "The request arrived, so the workflow ran" → a filtered delivery, an ignored bot and a repeated delivery are all acknowledged with 200 and queue nothing → confirm the run with `kilasflow exec list --workflow <workflowId> --trigger webhook`, and read the delivery table in references/WEBHOOK_DELIVERY.md.
- "I need an idempotency key on the manual run" → no verb carries one, and a manual retry is a second execution → make the workflow's own steps repeatable, or send `Idempotency-Key` through the escape hatch.
- "The schedule exists in the API, so it will fire" → a schedule row a trigger owns is created by activation and removed by deactivation, and a row created through the schedules API is never touched by either → activate for the trigger's own intervals, and keep hand-made rows to workflows with no schedule trigger.
- "The webhook ran my latest edit" → deliveries run the active revision, so an unsaved or unactivated edit is invisible to the caller → activate again, or test the latest revision with `kilasflow run`.
- "The triggered run fired every trigger" → a bare run starts every root in the document → name the one you meant with `--trigger <nodeId>`.

## Reference files

| File | Read when |
| --- | --- |
| WEBHOOK_DELIVERY.md | a caller's request reached a webhook route and you need to know what it was answered with, why it was not answered, or whether it ran the workflow once |
