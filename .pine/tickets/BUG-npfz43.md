---
id: BUG-npfz43
title: Editing a credential saves mask bullets as part of the secret
status: testing
priority: critical
labels:
    - credentials
    - security
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T13:43:42Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:ui-ops-surfaces.

---
### Typing into a masked secret field while editing a credential saves the mask bullets as part of the secret [find:ui-ops-surfaces] (critical/bug) · area: credentials · confidence: high

The edit dialog puts the server's placeholder "••••••••" into the password input as its real value. If you click in and type, as you would to fix a token, the bullets stay in front of the new text and are saved as the secret. There is no warning.

Evidence: On shared :8090, created "[ui-ops-surfaces] mask-test" (httpBearerAuth, token "orig-secret") via the API and used it in workflow "[ui-ops-surfaces] mask echo" (HTTP GET to the :8099 stub /echo, which echoes the Authorization header). Run 1 returned body.auth "Bearer orig-secret". In /credentials, clicked Edit: document.getElementById('credential-field-token').value is "••••••••". Clicked the field, pressed End, typed X: the value became "••••••••X", and Save credential succeeded. Run 2 returned body.auth "Bearer ••••••••X". The first audit run hit the same corruption ("Bearer ••••••••NEW"). Screenshot: 23-cred-edit-typed.png. Code: credentials/+page.svelte openEdit() copies credential.fields, including the placeholder, into the <Input type=password value=...>. The server keeps the stored secret only when the value equals the placeholder exactly.

n8n behavior: In normal editing, n8n's stored-secret placeholder never ends up inside a saved value.

Impact: Rotating a token the natural way silently breaks every workflow that uses the credential. The failure only shows up later as 401s from the third-party API, and the password field hides the leftover bullets.

Suggested fix: Render secret fields empty with a "Stored — type to replace" placeholder. Send the keep-stored marker only when the field was not touched, or clear the field on focus. Have the server reject secrets that contain the mask sequence.

Files: web/src/routes/(dashboard)/credentials/+page.svelte, internal/api/handlers (credential update), internal/credentials

## Acceptance criteria

- [ ] Typing into a masked secret field while editing a credential saves the mask bullets as part of the secret
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)