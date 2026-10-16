---
id: FEAT-8752vx
title: 'Credential usage: warn on deleting an in-use credential; flag and refuse dangling references'
status: todo
priority: medium
labels:
    - credentials
    - safety
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

Deleting a credential in use gives no warning, activation still succeeds, and the run fails with `repository record not found: credential`.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-13). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The credential list shows where a credential is used. The NDV marks a missing credential with an issue badge ("Credentials not set" / "Credential not found"), and the workflow can't be executed or activated cleanly.

# Steps to Reproduce

1. Create workflow "[ux-ops] uses header credential" whose HTTP node references "[ux-ops] Header auth". 2. /credentials → Delete that credential. 3. Reopen the workflow and select the node. 4. Activate the workflow, then run it.

# Expected

The delete dialog lists the workflows using the credential, the NDV flags "Credential deleted — pick another", activation refuses dangling references, and the runtime error names the missing credential.

# Actual

The dialog says only "This destroys the encrypted secret. Workflows using it will fail until they are given another credential." It gives no count and no names, and DELETE returns 204. In the NDV the HTTP Header Auth select is blank, with the Test button still shown and no warning. Activation succeeds (200, active:true). The run then fails with `execute node "h": node "Call API": resolve credential: repository record not found: credential`, which names neither the credential nor the fix.

# Acceptance Criteria
- [x] GET /credentials/{id} returns the workflows referencing it, and the delete dialog lists them — `usedBy` (unscoped callers, single read only) and the dashboard dialog: `TestGetCredentialListsTheWorkflowsUsingIt`, `TestGetCredentialHidesUsageFromAScopedKey`, `credential-usage.test.ts`; live UI check listed "Partner sync — Nodes: Call partner" (Track D, FEAT-qae4sh).
- [x] The NDV flags "Credential deleted — pick another" — the node panel asks the server about each credential id the node names that the picker list lacks (`probeCredentials`, GET /credentials/{id}) and flags only a 404, with `role="alert"` copy `properties_credential_deleted` (en + id), `aria-invalid`/`aria-describedby` on the select and the Test button withdrawn; the picker stays open to choose another or None: `credentials.test.ts` (probeCredentials), `properties-panel.test.ts` ("a node whose credential the server deleted"); `pnpm check` 0 errors, `pnpm test` 1185 passed.
- [x] Activation refuses unknown credential ids with a named diagnostic — `refuseUnknownCredentials` in `publish()`: 422 `config.invalid` at `/nodes/N/credentials/<type>`, naming node, node id, type and credential id; enabled nodes only. `TestActivationRefusesAnUnknownCredential`; live: activate answered 422 `node "Call partner" (http) attaches httpHeaderAuth credential "cred_…", which does not exist in this workspace: attach another credential to this node`.
- [x] The runtime error names the missing credential and the node — `node "<name>": credential "<id>" no longer exists in this workspace: attach another credential to this node` (still unwraps to ErrNotFound): `TestAMissingCredentialIsNamedInTheRunError`; live: a manual run of the dangling workflow failed with exactly that text for node "Call partner".

## Progress (2026-10-09)

Three of the four criteria were delivered by Track D with FEAT-qae4sh (commit f8253594): see that ticket's Progress. The NDV flag is deliberately deferred: Tracks C and G both edit the NDV this wave, and the embed listing is restricted to the session's confinement, so "not in the listing" would falsely flag a credential as deleted there. It needs either a per-id existence probe or the activation diagnostic surfaced in the editor; do it after C and G merge. This ticket stays open for that one criterion.

## Progress (2026-10-16)

The last criterion is done in `web/src/lib/components/workflow-editor/properties-panel.svelte` with `probeCredentials` / `credentialPresenceKey` / `credentialGone` in `web/src/lib/workflow-editor/credentials.ts`.

Why a per-id probe and not "absent from the list": the list proves nothing about absence. An embed session's listing is restricted to the credentials its grant names (`Credentials.List` filters by `session.Confinement.Credentials`), and the dashboard's `onMount` load swallows a failure into `[]`, so in either case an id missing from the picker can still be a live credential. The panel therefore treats a node's credential id as deleted only when GET /credentials/{id} answers 404 (`repository.ErrNotFound`), which is the authoritative signal and the same one the activation check and the run error already use. Ids that are in the list are never probed. Every other outcome (403, 401, 503, a dropped connection, no answer yet) is `unknown` and flags nothing.

Embed consequence, deliberately accepted: the embed middleware refuses GET /credentials/{id} (403, "This embed session cannot use this endpoint."), so an embedded editor never shows the flag. That is the safe direction, since it cannot tell deleted from out of grant there; the activation 422 and the run error still name the credential. Opening that endpoint to embed sessions would need a confinement-aware existence answer and is not done here.

The canvas and compiler side has no per-node credential diagnostic in the web editor (the activation diagnostic is a server 422 at publish time), so there was nothing to reuse; the flag is the NDV-side counterpart. The probe is one TanStack query keyed by the sorted ids it asks about, so each panel mount costs at most one request per unlisted id and the 30 s query-client stale time caches it.

Copy lives in `web/messages/{en,id}/properties.json` (`properties_credential_deleted`). The verdict literal stays in `credentials.ts` because `copy.test.ts` rejects a quoted catalog echo in a `.svelte` file.

# Implementation Plan

Add a usage query (workflow documents referencing the credential id) to GET /credentials/{id}, show it in the delete dialog, and add a compiler check for unknown credential ids at activate time.

# Notes

Related tickets: BUG-esb9sh

Related (from the audit): BUG-esb9sh (done). Its progress notes list "credential usage count (needs API)" as remaining, but it was closed.

# Related Files

agents/ux-ops/17-cred-delete-in-use.png, agents/ux-ops/18-ndv-dangling-credential.png; execution exec_01a0cbde-2a71-7d04-ac6e-fab996661bc5.

# Attachments
