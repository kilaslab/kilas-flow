---
id: BUG-cnhq1f
title: A webhook whose Respond node is skipped by the no-items rule answers with no body; live-backend-api CRUD spec fails since BUG-4ch186
status: done
priority: medium
labels:
    - e2e
    - webhooks
    - regression
    - parity
created: "2026-09-25T14:24:11Z"
updated: "2026-10-08T16:08:04Z"
---

# Description

`e2e/tests/live-backend-api.spec.ts:168` "four REST-shaped CRUD endpoints over one datastore share their rows" fails from c9cce97 (BUG-4ch186) onward. That commit's rule is: "a node the branch before it sent nothing is not run, whatever its alwaysOutputData".

The spec's last step deletes the row and then GETs it. The Data table node returns no rows, so the Respond to Webhook node (`respondWith: firstIncomingItem`) is no longer run. The caller gets a 200 with no JSON body, where the spec expects the empty item `{}`. The spec's own comment describes the old contract: "the Respond node answers with the empty item rather than leaving the caller without an answer".

Bisected on 2026-09-25: it passes at c9cce97^ and fails at c9cce97. It passed twice at 17b6d38 and fails at every later commit tried.

# Expected

Decide which contract holds for a webhook whose Respond node is skipped because its branch produced nothing, and check what n8n does. Either the caller gets the empty item as before, or the spec and the docs are updated with n8n's behaviour.

# Acceptance Criteria
- [x] n8n's behaviour for this case is recorded in the ticket in our own words. See "n8n's rule" below.
- [x] Either the engine answers the waiting webhook caller, or the spec expects the new answer and the docs say so. The engine already gives n8n's answer, so the spec changed. The culprit is c9cce97 (BUG-4ch186), confirmed by the ticket's own bisect. That commit was right: it brought KilasFlow to n8n's rule, and the spec was relying on the old behaviour.
- [x] The spec passes.

## n8n's rule (read on 2026-10-08 from n8n master on GitHub; behaviour only, written in our own words)

Sources: `packages/cli/src/webhooks/webhook-helpers.ts` (`executeWebhook`, `setupResponseNodePromise`), `webhook-request-handler.ts` (`sendLegacyResponse`), `webhooks/constants.ts`, and n8n commit f9814b7 (#37006, 2026-08-27) with its tests.

- A node whose parent output carries no items is not added to the run, so a Respond to Webhook node after a lookup that matched nothing never runs. Its own Always Output Data does not change that (the same rule BUG-4ch186 adopted).
- In "Using Respond to Webhook node" mode, the HTTP request waits for the execution. If the execution ends without the Respond node having answered, the post-execute handler answers instead:
  - If the run failed, the caller gets `500 {"message":"Error in workflow"}`.
  - If the run succeeded, the caller gets the Webhook node's response code (200 by default; the option is hidden in this mode) and **no body**. n8n's own test is named "responds with an empty body when a successful execution never reached the node".
- Before f9814b7, the execution's response promise was resolved with `{}` when the run ended. That also produced an empty answer, but it raced the 500 for a failed run. f9814b7 swapped `{}` for a sentinel so that only the post-execute handler decides.
- n8n's docs page for Respond to Webhook still says "a standard message with a 200 status". The code and tests send an empty body, and the code is what callers see.
- The n8n-idiomatic way to make such an endpoint answer JSON anyway is to set Always Output Data on the node whose empty result stops the branch (the Data table node here). That node then hands on one empty item, and the Respond node answers with it.

## Fix

KilasFlow already matched n8n. `respondFromExecution` answers `200` with an empty body when a successful responseNode run reached no Respond node, and `docs/src/content/docs/concepts/webhooks.md` already said so. The spec was the outlier: it put Always Output Data on the Respond node, which BUG-4ch186 (correctly) stopped honouring as a reason to run.

- `e2e/tests/live-backend-api.spec.ts`: the CRUD shapes put Always Output Data on the Data table node, so the delete-then-GET step still answers `{}`. A fifth endpoint runs the same GET with the flag on the Respond node and asserts n8n's answer: `200`, empty body, run succeeded, Respond node `skipped`.
- `docs/src/content/docs/concepts/webhooks.md`: a Respond node counts as not reached when its branch delivered no items, whatever its own Always Output Data. For a JSON answer either way, the flag belongs on the node before it.
- Go guard `internal/webhook/webhook_test.go` `TestWebhookWhoseRespondNodeIsSkippedAnswersAsN8nDoes`: webhook (responseNode) → Filter that matches nothing → Respond (firstIncomingItem). With the flag on the Respond node, the answer is 200 with an empty body and the Respond node is skipped. With the flag on the Filter, the answer is 200 with `{}` and the Respond node ran. The engine was already right, so this pins the contract. The first case is what changed at c9cce97: the ticket's bisect shows the e2e equivalent answering `{}` at c9cce97^.

Verified:
- `go vet ./internal/webhook/` and `go test ./internal/webhook/` pass.
- `npx playwright test tests/live-backend-api.spec.ts -g "four REST-shaped CRUD endpoints"`: 1 passed.
