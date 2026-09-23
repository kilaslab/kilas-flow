---
id: FEAT-bfrkyk
title: '''Listen for test event'' for webhook-triggered workflows (Execute currently runs with an empty item)'
status: todo
priority: medium
labels:
    - editor
    - webhooks
    - debugging
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

Execute on a webhook workflow runs at once with `{}` and reports success, building a green run on no data.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-14). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Execute workflow with a Webhook trigger listens on `/webhook-test/<path>` and waits for one real request, whose headers, query and body become the trigger output for building and debugging the flow.

# Steps to Reproduce

1. Open `[ux-debug] f webhook` and click **Execute**. 2. `curl -X POST http://127.0.0.1:18080/webhook-test/ux-debug-hook`.

# Expected

Listen for a test event, or at least prompt for a sample JSON payload, instead of a silent empty run.

# Actual

The run finishes at once with `Run succeeded.`. The Webhook output is `{}`, and `Echo` gets `{"got":null,"missing":null}`, a green run built on no data. `/webhook-test/...` answers 405. The only way to feed a sample payload is the CLI (`kilasflow run <id> --input '{"body":{"msg":"hi"}}'`, which works); the UI has no input field.

# Acceptance Criteria
- [ ] Execute on a webhook workflow enters a "waiting for test event" state on a short-lived test route, and the first request becomes the trigger output
- [ ] Alternatively or additionally, a sample-input dialog posts `input`
- [ ] An empty manual run of a webhook trigger is never silently green

# Implementation Plan

Add a short-lived test route plus a "Listen for test event" state in the editor, or a sample-input dialog that posts `input`.

# Notes

Related tickets: BUG-cq4yk3, FEAT-56nep4, FEAT-jvembs

Related (from the audit): FEAT-56nep4 (done; "cannot listen for a test event" still reproduces), BUG-cq4yk3, FEAT-jvembs

# Related Files

`$SP/agents/ux-debug/f-03-execute-webhook.png`, `cli/get_f_manual.json`, `cli/run_f_input.json`.

# Attachments
