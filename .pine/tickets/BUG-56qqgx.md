---
id: BUG-56qqgx
title: Webhook node panel shows /webhook/<path>, which 404s; the real address is the minted hash route
status: done
priority: high
labels:
    - editor
    - webhooks
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-26T16:54:44Z"
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
- [x] The panel reads `GET /workflows/{id}/webhooks` and shows the node's full minted URL with a copy button once bound
- [x] Before activation, it says clearly that no URL exists yet, instead of showing a path-based one

## Fix (2026-09-26)

The Webhook node panel no longer builds an address from the typed path. It reads `GET /workflows/{id}/webhooks` (`web/src/lib/components/workflow-editor/properties-panel.svelte`; the pure rules are in `web/src/lib/workflow-editor/webhook-address.ts`, the rendering in `webhook-address.svelte`) and shows the node's minted route under `window.location.origin` with a Copy URL button, reusing the copy pattern of the import report and the activation notices.

One deliberate difference from the ticket's wording: the endpoint mints the route on first read and reuses it forever, so the URL is real before activation and the activation does not change it. The panel therefore shows it before activation too, marked "Not live yet — this URL starts answering once the workflow is activated", and "Live" once the workflow is active. What it never shows is a guess: a node that was never saved, or whose path was typed after the last save, says the address appears on save; a node with no path asks for one; a failed lookup is local to the box and offers a retry. The lookup is skipped for a node the server would not bind (unsaved, no path, disabled), and it is keyed by node with `staleTime: 0` so a node saved after an earlier answer is not looked up in the old one.

The address block sits outside the panel's `inert` region, so a read-only viewer can still select and copy it. The obsolete `properties_webhook_full_address` message is removed and `properties_webhook_set_path` reworded (en and id).

There is one URL because the API returns one: no Test URL is invented. The origin is the browser's, not `server.public_url`, because the API does not expose the latter and the editor, the API and `/webhook` share an origin in production, the dev proxy and embeds.

Checked live against a built binary: an inactive workflow shows `http://<host>/webhook/<minted>` with "Not live yet"; after activation the panel reads "Live" and `POST` to that URL answers 200, while `POST /webhook/<path>` answers 404 "No active workflow is bound to this webhook.".

# Implementation Plan

Read `listWorkflowWebhooks` in the panel and show `url` for the node.

# Notes

Related tickets: BUG-cq4yk3, FEAT-56nep4

Related (from the audit): FEAT-56nep4 (done; "Show the production URL… once bound" is implemented with the wrong value), BUG-cq4yk3

# Related Files

`$SP/agents/ux-debug/f-01-webhook-panel.png`, `f-02-activated.png`. web/src/lib/components/workflow-editor/properties-panel.svelte:190-200 (renders `/webhook/${pathParam}`), internal/webhook/lifecycle.go:225 (`"/webhook/" + binding.Route`).

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-26. The generated file list diffed from the ticket's creation commit (a bulk audit commit of 809 files), so it is replaced here by hand with this ticket's own commit.

- Commits (1):
  - `9d17d7f` — BUG-56qqgx: the Webhook node panel shows the minted URL that answers, with its host and a copy button, not /webhook/<path>
- Files changed (`git show --stat 9d17d7f`):

```
 CHANGELOG.md                                                      |   8 +
 web/messages/en/properties.json                                   |   9 +-
 web/messages/id/properties.json                                   |   9 +-
 web/src/lib/components/workflow-editor/panel-query-harness.svelte |  19 +
 web/src/lib/components/workflow-editor/properties-panel.svelte    | 155 ++++----
 web/src/lib/components/workflow-editor/properties-panel.test.ts   | 174 +++++++++
 web/src/lib/components/workflow-editor/webhook-address.svelte     |  91 +++++
 web/src/lib/components/workflow-editor/webhook-address.test.ts    |  63 +++
 web/src/lib/components/workflow-editor/workflow-editor.svelte     |   7 +-
 web/src/lib/workflow-editor/webhook-address.test.ts               | 151 +++++++
 web/src/lib/workflow-editor/webhook-address.ts                    |  82 ++++
 11 files changed, 704 insertions(+), 64 deletions(-)
```

