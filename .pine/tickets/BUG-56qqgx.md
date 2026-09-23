---
id: BUG-56qqgx
title: Webhook node panel shows /webhook/<path>, which 404s; the real address is the minted hash route
status: todo
priority: high
labels:
    - editor
    - webhooks
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

A user debugging "why doesn't my webhook fire" is shown a URL that can never work.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-13). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The Webhook NDV shows the Test and Production URLs that actually work, ready to copy.

# Steps to Reproduce

1. Open `[ux-debug] f webhook` (path `ux-debug-hook`), activate it, and select the Webhook node. 2. `curl -X POST http://127.0.0.1:18080/webhook/ux-debug-hook -d '{"msg":"hi"}'`. 3. `GET /api/v1/workflows/{id}/webhooks`.

# Expected

The panel shows the minted URL from `GET /workflows/{id}/webhooks`, with the host, once bound, or says clearly that no URL exists until activation.

# Actual

The panel's WEBHOOK URL box shows `/webhook/ux-debug-hook`, with the text "The full address is shown after import and on activation". That URL answers `404 {"detail":"No active workflow is bound to this webhook."}`. The real binding is `/webhook/3225f5b5373e2bc0d5ba95b12512469f` (list-workflow-webhooks), which works. A user debugging "why doesn't my webhook fire" is shown the wrong URL.

# Acceptance Criteria
- [ ] The panel reads `GET /workflows/{id}/webhooks` and shows the node's full minted URL with a copy button once bound
- [ ] Before activation, it says clearly that no URL exists yet, instead of showing a path-based one

# Implementation Plan

Read `listWorkflowWebhooks` in the panel and show `url` for the node.

# Notes

Related tickets: BUG-cq4yk3, FEAT-56nep4

Related (from the audit): FEAT-56nep4 (done; "Show the production URL… once bound" is implemented with the wrong value), BUG-cq4yk3

# Related Files

`$SP/agents/ux-debug/f-01-webhook-panel.png`, `f-02-activated.png`. web/src/lib/components/workflow-editor/properties-panel.svelte:190-200 (renders `/webhook/${pathParam}`), internal/webhook/lifecycle.go:225 (`"/webhook/" + binding.Route`).

# Attachments
