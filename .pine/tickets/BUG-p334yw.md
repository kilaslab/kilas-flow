---
id: BUG-p334yw
title: Error workflow only runs if itself active; payload lacks execution.url and uses node id for lastNodeExecuted
status: done
priority: medium
labels:
    - engine
    - error-handling
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-10-08T16:20:00Z"
---

# Description

n8n runs an Error Trigger workflow without activation. KilasFlow silently skips it, and only the server log notices.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-15). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** "If a workflow uses the Error Trigger node, you don't have to publish the workflow" (docs.n8n.io Error Trigger). The payload has `execution.url`, `execution.lastNodeExecuted` (the node name) and `execution.error.stack`.

# Steps to Reproduce

1. `[ux-debug] g failing webhook` (Webhook → Stop and Error, `settings.errorWorkflow` = `[ux-debug] g error handler`, which is inactive). Activate g-main and POST its webhook. 2. Activate the error handler and POST again.

# Expected

Error workflows run without activation (or activation is enforced when the setting is saved). The payload matches n8n: node name, url, stack. The failed execution shows whether its error workflow ran.

# Actual

Step 1: nothing runs for the handler, the failed execution's UI does not mention it, and the only trace is `level=ERROR msg="error workflow failed" … error="repository record not found: active workflow"` in the server log. Step 2 works, but `lastNodeExecuted` is the node id (`"stop"`), not the name "Stop", and `execution.url` is absent, so the usual "open the failed run" link in an alert cannot be built. The handler run is listed with trigger `subworkflow`.

# Acceptance Criteria
- [x] The error workflow's latest revision runs whether or not it is active (or saving the setting enforces activation, with a clear message)
- [x] The payload has `execution.url`, `lastNodeExecuted` as the node *name*, and `error.stack`
- [x] The failed execution records and shows the error-workflow execution it triggered

# Implementation Plan

Resolve the error workflow's latest revision whether or not it is active, and add `url` and the node name. Record the error-workflow execution id on the failed execution.

# Notes

Related tickets: BUG-aede06

Related (from the audit): BUG-aede06 (done, introduced the mechanism)

# Related Files

`$SP/kf-server.log` line 4997, `$SP/agents/ux-debug/cli/get_g_err.json`, `g-01-error-wf-activate.png`. internal/engine/service.go:1185-1215 (`errorWorkflowItem`).

# Attachments

## Fix (2026-10-08)

1. Runs without activation: `runErrorWorkflow` (internal/engine/service.go) starts the error workflow with `ChildExecution.LatestRevision`, so `StartChild` (internal/repository/executions.go) resolves the workflow's latest revision whether or not it is active. An edit to an active error workflow takes effect when it is saved. A sub-workflow call from Execute Workflow still needs an active revision; only the error path changed.
2. Payload: `execution.lastNodeExecuted` is the failed node's name. `execution.url` is `<server.public_url>/executions/<id>`, the dashboard page (`engine.ExecutionPath`), and path-only when no public URL is configured, like the resume links. `execution.error.stack` is set. A Go failure has no JavaScript stack, so it holds the message plus `at <node name> (<node type>)`.
3. Recorded and shown: after the error workflow is started or refused, the new `AnnotateFailure` store method adds `errorWorkflowId` to the failed execution's `error` object, then `errorExecutionId` for the run it started or `errorWorkflowError` for why none started (self-reference, missing workflow, start failure). Existing keys are never overwritten. The execution page's error card already lists an error object's scalar fields, so the id appears there, in `exec get` and in the API. It is shown as text, not as a link.

Not changed: the handler run is still listed with trigger `subworkflow` (it carries `parentExecutionId`). A separate `error` trigger kind (a new enum value and the history filter that lists it) is left for a follow-up.

skills/kilasflow-error-handling/SKILL.md no longer says an error workflow must be active, and it names the new fields.

Tests: `TestAnErrorWorkflowRunsItsLatestRevisionWhetherOrNotItIsActive` (never activated; activated then edited; a missing target) and new assertions in `TestAFailedRunStartsTheErrorWorkflowNamedInItsSettings` (node name, url, stack, recorded execution id, the self-reference refusal recorded) in internal/engine/error_workflow_test.go. Both failed first. `go test ./internal/engine/ ./nodes/ ./internal/repository/ ./internal/skills/` and `go vet` on those packages pass.
