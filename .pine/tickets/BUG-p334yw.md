---
id: BUG-p334yw
title: Error workflow only runs if itself active; payload lacks execution.url and uses node id for lastNodeExecuted
status: todo
priority: medium
labels:
    - engine
    - error-handling
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
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
- [ ] The error workflow's latest revision runs whether or not it is active (or saving the setting enforces activation, with a clear message)
- [ ] The payload has `execution.url`, `lastNodeExecuted` as the node *name*, and `error.stack`
- [ ] The failed execution records and shows the error-workflow execution it triggered

# Implementation Plan

Resolve the error workflow's latest revision whether or not it is active, and add `url` and the node name. Record the error-workflow execution id on the failed execution.

# Notes

Related tickets: BUG-aede06

Related (from the audit): BUG-aede06 (done, introduced the mechanism)

# Related Files

`$SP/kf-server.log` line 4997, `$SP/agents/ux-debug/cli/get_g_err.json`, `g-01-error-wf-activate.png`. internal/engine/service.go:1185-1215 (`errorWorkflowItem`).

# Attachments
