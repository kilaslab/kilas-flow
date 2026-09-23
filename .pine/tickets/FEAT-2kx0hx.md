---
id: FEAT-2kx0hx
title: 'Embed sessions deep-dive: scopes, confinement, lifetime & refresh, origins & CORS; generated permit table'
status: todo
priority: high
labels:
    - docs
    - embedding
    - security
parent: EPIC-62zt4j
created: "2026-09-23T02:04:34Z"
updated: "2026-09-23T02:04:34Z"
---

# Description

An integrator can't predict what an embedded user may do. Confinement is undocumented, the concept and contract pages describe three workflow scopes only, datastore sessions are missing, and the generated reference marks allowed datastore row operations "Deny".

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-2, DOC-3, DOC-4, DOC-10, DOC-11, DOC-24, DOC-5). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

# Findings

## DOC-2: Embed-session confinement is not documented on the docs site, so integrators cannot predict which credentials and tables an embedded user may attach

*docs · high · embedding*

**Steps to reproduce:**

1. `grep -rn -i confine docs/src/content/docs` finds only prose ("confines an editor to one workflow") and no description of the mechanism.
2. Read `internal/api/handlers/embedscope.go:55-75` and `internal/embed/confinement.go:19-33`. A workflow session carries a confinement derived from the owner's active revision (or the latest draft). The session may attach only credentials, data tables and sub-workflows that revision already references. The credential picker is filtered to those ids (`embedAllowsCredential`). A save, publish, restore or run that reaches outside the confinement is refused with a 403 that names the offending nodes.

**Actual:**

The embedding guide (`guides/embedding.md`), the tenancy concept page and the API contract never mention this. The only accurate description is in `skills/kilasflow-embedding/references/SESSION_AUTHORITY.md`. An integrator whose end users are expected to "add a Slack credential in the embedded editor" finds out from a 403.

**Expected:**

A section on the docs site covering what confinement is, how it is derived (active revision, then latest draft), the recipe "pre-attach credentials and data tables server-side, then mint", what the picker shows, and the exact refusal text.

**Suggested fix:**

Add an "Embed sessions: scopes, confinement, lifetime" page and link it from the quickstart and the troubleshooting table.

**Evidence:**

the code references above. `guides/embedding.md:223` says only "Values are never returned to a picker beyond names".

**Related:**

BUG-9853ay (done; it introduced confinement without a docs update).


## DOC-3: The tenancy concept page and the API contract describe three workflow scopes and one-workflow sessions; datastore sessions and scoped keys are missing

*docs · high · embedding*

**Steps to reproduce:**

1. `concepts/tenancy-and-embedding.md:130-143` says "The payload names one tenant, exactly one workflow" and "Three scopes exist: workflow:read, workflow:write and workflow:run".
2. `reference/api-contract.md:313` says "An embed session is confined to one workflow and its executions". Its table (lines 317-330) has no datastore rows.
3. `curl -s :18080/api/openapi.json`: `EmbedSessionBody.scopes` documents `workflow:read, workflow:write, workflow:run, datastore:read, datastore:write`, and `datastoreId` is an alternative subject (`internal/embed/embed.go:32-40`).

**Actual:**

The concept and contract pages are stale since datastore sessions shipped. The "What a session may actually do" table (tenancy page lines 214-226) omits `/datastores/**`. The "API operations" paragraph (line 294-303) omits `DELETE /tenants/{id}`.

**Expected:**

Five scopes with their implication rules, two subject kinds, and a permit table that matches `internal/api/middleware/scope.go`.

**Suggested fix:**

Regenerate or rewrite both tables from `permits()`. Consider generating the permit table from code (see DOC-4).

**Evidence:**

the file and line references above, and the OpenAPI schema dump in `$SP/agents/docs-saas/openapi.json`.

**Related:**

FEAT-1c70nt (done; it opened datastores to embedded hosts without updating these pages).


## DOC-4: The generated API reference marks datastore row operations "Embed: Deny", but datastore sessions are allowed to call them

*docs · high · api-reference*

**Steps to reproduce:**

1. Open `docs/src/content/docs/reference/api/datastores.md`. `insert-datastore-row`, `get-datastore-row` and `list-datastore-rows` all read "Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor."
2. Compare with `internal/api/middleware/scope.go:286-340`. GET on `/datastores/{id}`, `/rows` and `/rows/{rowId}` is allowed with `datastore:read`, and POST, PUT or DELETE under `/rows` (insert, upsert, increment, update, delete and CSV import) is allowed with `datastore:write`.
3. `scripts/generate-api-reference.mjs:91-121` (`embedVerdict`) is a hand-written mirror of an older `permits()` that has no datastore, scoped-key or `/auth/me` branches. Its comment still points at `internal/api/middleware/embed.go`.

**Actual:**

Wrong verdicts ship in a page labelled "Generated … not hand-written". The deny reason is the same generic sentence for every refused operation, including `get-health` and `get-ready`. The Embed group blurb says "confines an embedded editor to one workflow" (line 61).

**Expected:**

Verdicts that match the server, with a per-operation reason.

**Suggested fix:**

Export the permit table from Go (for example a test that dumps a JSON verdict per operation id) and have the generator read it. The drift gate then covers it.

**Evidence:**

the file and line references above.

**Related:**

FEAT-za118x (done).


## DOC-10: The embedding guide says import is the only call that reports a webhook address; `GET /workflows/{id}/webhooks` returns it for a created workflow

*docs · medium · embedding*

**Steps to reproduce:**

1. `guides/embedding.md:52-56`: "The import response is the only call that reports the minted webhook address back — activation reports nothing." The same claim appears in `sdk/examples/reference-host/README.md:93-96` and `server.mjs:228-234`.
2. On :18190: `POST /api/v1/workflows` with a `kilasflow.webhook` node, then `GET /api/v1/workflows/{id}/webhooks` → `[{"nodeId":"hook","method":"POST","path":"docs-saas-hook","url":"/webhook/36feb9ba…"}]` before activation. Reproduced on two workflows.

**Actual:**

The guide's advice to "Import, rather than create" rests on a premise that stopped being true when FEAT-cwmw90 shipped.

**Expected:**

The guide recommends `listWorkflowWebhooks` (SDK) or `GET …/webhooks`.

**Suggested fix:**

Update the guide, the reference host and its README.

**Evidence:**

the steps above. `concepts/webhooks.md:27-36` already documents minting on read.

**Related:**

FEAT-cwmw90 (done)


## DOC-11: Four sources describe the datastore embed surface differently, and none matches the server

*docs · medium · datastores*

**Steps to reproduce:**

1. `sdk/README.md:113-117`: the embed half is "`getDatastore`, `listDatastoreRows`, `getDatastoreRow`, `insertDatastoreRow`".
2. `sdk/examples/reference-host/README.md:136-140`: "the CSV import — stays with the backend key".
3. `internal/api/middleware/scope.go:326-337`: every POST, PUT or DELETE under `/datastores/{id}/rows` (insert, upsert, increment, bulk update and delete, and `rows/import`) is allowed with `datastore:write`, and GET (including `rows/export`) with `datastore:read`.
4. `guides/embedding.md:372-375`: "The reference host … swaps in this node once it lands". `kilasflow.datastore` ("Data table") is in the live catalogue. `sdk/examples/reference-host/server.mjs:330-333` says "The SDK has no datastore methods yet".

**Actual:**

The descriptions are contradictory and stale.

**Expected:**

One table of datastore-session permissions, matching `permits()`.

**Suggested fix:**

Fix all four sources from the generated permit table (DOC-4).

**Evidence:**

the file and line references above, and `curl :18080/api/v1/node-types` (shows `kilasflow.datastore 1 Data table`).

**Related:**

FEAT-1c70nt, FEAT-nc6z9r (both done)


## DOC-24: CORS behaviour for embed origins is undocumented

*gap · low · embedding*

**Steps to reproduce:**

1. `internal/api/middleware/cors.go:1-60`: `embed.allowed_origins` doubles as the CORS allowlist, credentials are never allowed, and the exposed headers are `X-Request-ID, X-Next-Cursor, Idempotent-Replayed, Retry-After`.
2. `grep -rn -i cors docs/src/content/docs` finds only "no CORS to think about" and webhook preflight notes.

**Actual:**

A host page that calls the API directly with a datastore session token, or opens `EventSource`, has no documentation of what is allowed.

**Expected:**

A short CORS section on the embed-sessions page.

**Suggested fix:**

Document it.

**Evidence:**

the steps above.

**Related:**

BUG-fv5fer (done)


## DOC-5: No documented or supported way to refresh an embed token in a mounted editor; sessions are capped at 30 minutes

*gap · high · embedding*

**Steps to reproduce:**

1. Mint a session with `ttlSeconds: 2` and GET the workflow with the token: 200. Three seconds later: 401. Reproduced twice (throwaway server, `evidence/kf-auth-server-2.log`).
2. Mint with `ttlSeconds: 7200`: `expiresAt` is 30 minutes after mint (clamped, `internal/embed/embed.go:52`).
3. `grep -n -i "refresh\|updateSession\|renew" sdk/src/browser.ts` returns nothing. `MountedEditor` exposes only `iframe` and `unmount()`.
4. The frame keeps its `message` listener after the handshake (`web/src/lib/embed/session.svelte.ts:183-209`), so posting a fresh `kilasflow:embed-session` would probably swap the token, but nothing documents or supports that.

**Actual:**

The docs say "A host that needs a longer editing session mints another token" (`concepts/tenancy-and-embedding.md:150-153`, `guides/embedding.md:140-142`) and never say how to hand it to a live editor. The only visible option is unmount and remount, which loses unsaved edits. After expiry the editor shows a stale-data banner with 401.

**Expected:**

An SDK method (for example `editor.updateSession(session)`) or an editor event such as `session-expiring`, plus a documented refresh loop driven by `expiresAt`.

**Suggested fix:**

Add a supported re-handshake message and an SDK method, then document it on the embed-sessions page.

**Evidence:**

the steps above, and `sdk/src/browser.ts:26-66`.

**Related:**

none


# Acceptance Criteria
- [ ] The embed permit table (scope × operation) is exported from Go into the reference generator and drift-gated; datastore row operations show Allow where they are allowed
- [ ] Confinement is documented with the "pre-attach, then mint" recipe, an example and the exact 403 texts
- [ ] Session lifetime (a 30-minute cap) and the refresh procedure are documented, using FEAT-0xsc1s once it lands; until then, the limitation is stated
- [ ] Origins and CORS behaviour are documented, and the four inconsistent datastore-embed descriptions become one
- [ ] DOC-2, 3, 4, 10, 11, 24 and the docs half of DOC-5 are resolved
- [ ] Explain how confinement is derived from the saved revision, and how to seed documents (or use host-declared confinement, FEAT-r267jj)
- [ ] Schedules reference: resolve the "cron evaluated in UTC" wording against workflow timezones; document embed publish semantics (BUG-mzk0xn)

# Implementation Plan

See each finding's suggested fix above.

# Notes

Also from the host-SaaS integration review (D2, D3).

Related tickets: BUG-9853ay, BUG-fv5fer, FEAT-1c70nt, FEAT-cwmw90, FEAT-nc6z9r, FEAT-za118x

# Related Files

# Attachments
