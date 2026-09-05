---
id: FEAT-0556ck
title: Put n8n import and export in the editor
status: todo
priority: medium
labels:
    - interop
    - editor
deps:
    - FEAT-zmfsjd
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T12:00:23Z"
updated: "2026-09-05T12:00:23Z"
---

## Scope

n8n import and export work over the API, are covered by tests in `internal/api/workflows_test.go`, and **no user can reach either one**.

`POST /api/v1/workflows/import` accepts `{format, workflow, name}` and returns 201 with the saved workflow, an `unsupported` diagnostics array and a `webhooks` array carrying the freshly minted route for every trigger node. `GET /api/v1/workflows/{id}/export?format=n8n` returns the document plus `lossy` and `supportedMappings`. The orval client for both is generated and committed at `web/src/lib/api/generated/interop/interop.ts`, along with `importWorkflowInputBody`, `importedWorkflowResource`, `exportWorkflowParams` and `exportedWorkflowResource` model types.

Nothing calls any of it. The SPA has nine pages — the dashboard root, workflows list and editor, credentials, executions list and detail, schedules, settings, and the embed frame — and `web/src/routes/(dashboard)/app/workflows/+page.svelte` offers exactly one action: create workflow. A search across `web/src` for `importWorkflow` or `exportWorkflow` outside the generated directory returns nothing.

So the product's headline capability — bring your n8n workflows and run them here — is reachable only by constructing a JSON body and posting it with `curl`. The migration guide from V2-p10-19 would have to open by teaching a command line.

The diagnostics make this worse rather than better as a curl exercise, because they are the part that most needs a screen. An import returns structured `ImportIssue` values at three severities and an array of minted webhook URLs, and a user who does not read them carefully gets a workflow that saves, opens, looks correct and either cannot activate or receives nothing because the sending system still points at the old path. That is a report to be read, not a response body to be skimmed in a terminal.

## Acceptance criteria

- [ ] A user imports an n8n workflow export from the workflows list without leaving the browser, by file upload and by paste.
- [ ] The import result is presented as a report rather than a success toast: every diagnostic with its severity, the node it concerns, and what the user should do about it.
- [ ] Blocking, lossy and dropped diagnostics are visually distinct, and a workflow containing a blocking issue tells the user plainly that it will not activate and why.
- [ ] Every minted webhook URL is shown with its node and copyable, since the sending system must be re-pointed and the old path will not work.
- [ ] Unsupported nodes are identifiable in the editor after import, and selecting one shows the original type and version the capsule preserved.
- [ ] Export is reachable from the workflow editor, downloads a file, and surfaces the `lossy` list before the download rather than after.
- [ ] The screens use the generated interop client rather than hand-written fetch calls, so they stay in step with the API through the existing drift check.
- [ ] Import is absent from the embedded editor surface, matching `permits()`, which denies `/workflows/import` to an embed session outright.

## Implementation Plan

Sequence this after V2-p10-19 so the screen and the guide agree on vocabulary. The words used for the three severities and for the webhook re-pointing step should be written once and used in both; deciding them in the UI first and documenting them second is how a product ends up with a help page that describes different labels than the screen.

Put import on the workflows list beside create, since that is where a user with a file to bring arrives. Put export in the editor, where a user with a workflow to take away is.

The report screen is the real work and deserves the design attention. Recommend a full page or a large dialog rather than a toast: there can be many diagnostics, they have structure, and the webhook URLs must be copied. Group by severity with blocking first, and lead with the single most consequential sentence — whether this workflow can be activated as imported.

Two things to reuse rather than invent. `FEAT-ptyh9w` (V2-p9-8) extracts a shared list-page shell and a table primitive precisely because the loading skeleton, error card, empty state and `message(error)` helper are already copy-pasted across four route files; a diagnostics table is a fifth copy unless it uses that primitive. And `FEAT-sdjdh2` (V2-p8-1's neighbour) surfaces activation notices in the editor — the minted webhook URL is an activation notice by another name, and the two surfaces should not be independently designed.

One trap worth naming. `importWorkflowInputBody` carries the n8n document as an opaque JSON value, and a file upload gives a string. Parsing it in the browser to validate before sending is tempting and wrong: the importer is the authority on what is a valid n8n document and it produces good diagnostics, while a browser-side pre-check produces a second, worse set of error messages for the same input. Send it and render what comes back.

Keep this out of the embed surface entirely. `internal/api/middleware/embed.go` denies `/workflows/import` to an embed session with no scope able to grant it, so an import control in the embedded editor would be a button that always fails.

## References

- Roadmap plan, p10 section, entry V2-p10-20: `.pine/roadmap.md`.
- `internal/api/handlers/interop.go` — the import and export handlers, the response envelopes, and the minted webhook array.
- `web/src/lib/api/generated/interop/interop.ts` — the generated client that nothing calls.
- `web/src/routes/(dashboard)/app/workflows/+page.svelte` — the list page where import belongs.
- `web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte` — the editor where export belongs.
- `internal/interop/n8n/n8n.go` — `ImportIssue` and its severities, and `SupportedMappings()`.
- `nodes/unsupported.go` — the capsule whose preserved original type the editor should show.
- `internal/api/middleware/embed.go` — `permits()` denying `/workflows/import`.
- `.pine/tickets/FEAT-ptyh9w.md` — V2-p9-8, the shared list shell and table primitive to build on.
- `.pine/tickets/FEAT-sdjdh2.md` — activation notices, the neighbouring surface for minted webhook URLs.
- `web/scripts/check-api-client.mjs` — the drift check that requires using the generated client.
