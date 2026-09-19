---
id: BUG-mewhrd
title: Credential delete is one click; no confirm, no in-use warning, failures invisible
status: todo
priority: high
labels:
    - credentials
    - ux
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T12:06:09Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 3 finding(s) from dims: find:ui-ops-surfaces, find:ui-shell-lists, find:web-frontend-code.

---
### Credential delete is one click: no confirmation, no in-use warning, and a failed delete is never shown [find:ui-shell-lists] (high/bug) · area: credentials list · confidence: high

The trash button calls deleteCredential straight away, with no confirm dialog. The server also deletes credentials that workflows still reference, including active ones: credentials.go:284-292 has no usage check. One misclick therefore destroys an encrypted secret for good and quietly breaks live workflows. When a delete fails, remove() writes the message into formError. formError is only rendered inside the closed editor dialog, and openCreate()/openEdit() reset it, so the error never appears. Datastores do have a proper delete confirm, so the list pages behave inconsistently. This overlaps ui-ops-surfaces F6.

Evidence: Playwright on /credentials with DELETE /api/v1/credentials/cred_01a0af28… mocked to 409. Clicking 'Delete [ui-shell-lists] bearer B…' fired 1 DELETE immediately. [role=dialog|alertdialog] count was 0, [role=alert] on the page was [], and opening 'New credential' afterwards showed no alert. Screenshot: /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/13-cred-delete-409.png. Code: credentials/+page.svelte:98-105 (remove), 150-152 (button), 201 (formError only inside the dialog). datastores/+page.svelte:189-197 has a proper 'Delete X?' dialog.

Impact: Secrets can be lost with no recovery, and every workflow using them breaks silently. The same pattern affects schedules (see the schedules finding).

Suggested fix: Add a confirm dialog that names the credential and lists the workflows using it. This needs a usage lookup, or the API could return 409 when active workflows reference it unless forced. Show delete errors in a page-level alert or toast, and reuse the datastores confirm pattern.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/credentials/+page.svelte, /Users/izzadev/projects/k-flow/internal/api/handlers/credentials.go

---
### Credential delete is one click with no confirmation, no in-use warning, and failures are never shown [find:ui-ops-surfaces] (high/ux) · area: credentials · confidence: high

The trash icon deletes immediately, including credentials that workflows still use. Those workflows then fail with an internal-sounding error. If the delete itself fails, the error goes into the edit dialog's error text, and that dialog is closed.

Evidence: "[ui-ops-surfaces] mask-test" was used by "[ui-ops-surfaces] mask echo". One click on its trash icon and GET /credentials/{id} returned 404; no dialog appeared. Running the workflow then failed with "execute node \"big\": node \"Big\": resolve credential: repository record not found: credential". Code: credentials/+page.svelte remove() calls deleteCredential directly, and its catch sets formError, which only renders inside the closed Dialog (15-cred-delete-failure-silent.png). The Schedules page uses the same pattern. The Datastores page, by contrast, has a proper confirmation dialog.

n8n behavior: n8n asks "Are you sure you want to delete <name>?", and a node that references a missing credential shows an issue in the editor.

Impact: Secrets can be deleted by accident with no undo, workflows break at run time, and failed deletes are invisible.

Suggested fix: Add a confirmation dialog that names the credential and shows "Used by N workflows" from a server-side usage lookup. Show delete errors in a toast or alert. Change the runtime error to "credential <name> was deleted".

Files: web/src/routes/(dashboard)/credentials/+page.svelte, web/src/routes/(dashboard)/schedules/+page.svelte, internal/api/handlers (credentials)

---
### Credentials and Schedules delete on one click with no confirmation and hide the errors; credential defaults are not prefilled, so a Telegram credential cannot be created from the form [find:web-frontend-code] (medium/bug) · area: dashboard: credentials / schedules · confidence: high

remove() on the Credentials and Schedules pages deletes as soon as the trash icon is clicked. On failure it writes to formError, which renders only inside the editor dialog (closed at that point) and is reset when the dialog opens, so a refused delete is swallowed. The credential form starts with fields = {} and ignores CredentialField.default. telegramApi.baseUrl is required with default https://api.telegram.org, so the form's first save fails unless the user types the URL.

Evidence: routes/(dashboard)/credentials/+page.svelte:97-104 (remove), :149-151 (trash button), :193 (formError inside dialog), :51-58 (openCreate fields={}), :181. routes/(dashboard)/schedules/+page.svelte:80-86, trash button, formError inside the dialog. POST /api/v1/credentials {type:'telegramApi', fields:{accessToken:'123:abc'}} returns 422 'credential field "baseUrl" is required' (verified).

n8n behavior: n8n confirms deletes. Credential modals prefill defaults and test the credential on save.

Impact: One misclick deletes a production credential or schedule with no undo, errors go unseen, and Telegram (a core pack) credential creation fails for new users.

Suggested fix: Add a shared confirm dialog and a page-level error slot in ListStates. Prefill defaults when a type is chosen. Add a Test button (POST /credential-types/{type}/test and /credentials/{id}/test).

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/credentials/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/schedules/+page.svelte

## Acceptance criteria

- [ ] Credential delete is one click: no confirmation, no in-use warning, and a failed delete is never shown
- [ ] Credential delete is one click with no confirmation, no in-use warning, and failures are never shown
- [ ] Credentials and Schedules delete on one click with no confirmation and hide the errors; credential defaults ar
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)