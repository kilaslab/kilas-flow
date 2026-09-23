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
- [ ] GET /credentials/{id} returns the workflows referencing it, and the delete dialog lists them
- [ ] The NDV flags "Credential deleted — pick another"
- [ ] Activation refuses unknown credential ids with a named diagnostic
- [ ] The runtime error names the missing credential and the node

# Implementation Plan

Add a usage query (workflow documents referencing the credential id) to GET /credentials/{id}, show it in the delete dialog, and add a compiler check for unknown credential ids at activate time.

# Notes

Related tickets: BUG-esb9sh

Related (from the audit): BUG-esb9sh (done). Its progress notes list "credential usage count (needs API)" as remaining, but it was closed.

# Related Files

agents/ux-ops/17-cred-delete-in-use.png, agents/ux-ops/18-ndv-dangling-credential.png; execution exec_01a0cbde-2a71-7d04-ac6e-fab996661bc5.

# Attachments
