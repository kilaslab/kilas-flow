---
id: BUG-cnhq1f
title: A webhook whose Respond node is skipped by the no-items rule answers with no body; live-backend-api CRUD spec fails since BUG-4ch186
status: todo
priority: medium
labels:
    - e2e
    - webhooks
    - regression
    - parity
created: "2026-09-25T14:24:11Z"
updated: "2026-09-25T14:24:11Z"
---

# Description

`e2e/tests/live-backend-api.spec.ts:168` "four REST-shaped CRUD endpoints over one datastore share their rows" fails from c9cce97 (BUG-4ch186) onward. That commit's rule is: "a node the branch before it sent nothing is not run, whatever its alwaysOutputData".

The spec's last step deletes the row and then GETs it. The Data table node returns no rows, so the Respond to Webhook node (`respondWith: firstIncomingItem`) is no longer run. The caller gets a 200 with no JSON body, where the spec expects the empty item `{}`. The spec's own comment describes the old contract: "the Respond node answers with the empty item rather than leaving the caller without an answer".

Bisected on 2026-09-25: it passes at c9cce97^ and fails at c9cce97. It passed twice at 17b6d38 and fails at every later commit tried.

# Expected

Decide which contract holds for a webhook whose Respond node is skipped because its branch produced nothing, and check what n8n does. Either the caller gets the empty item as before, or the spec and the docs are updated with n8n's behaviour.

# Acceptance Criteria
- [ ] n8n's behaviour for this case is recorded in the ticket in our own words.
- [ ] Either the engine answers the waiting webhook caller, or the spec expects the new answer and the docs say so.
- [ ] The spec passes.
