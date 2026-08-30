---
id: FEAT-rcm205
title: Deliver API-first workflow CRUD and lifecycle versioning
status: doing
priority: critical
labels:
    - workflow
    - api
    - crud
    - versioning
deps:
    - FEAT-kk9h5y
parent: EPIC-c7gbdp
phase: p1
created: "2026-08-29T15:39:49Z"
updated: "2026-08-30T07:58:31Z"
---

## Scope

Implement REST workflow create, list, get, update, delete, activation/deactivation, and manual-run entrypoint around the canonical workflow contract. The API is authoritative; the editor will consume it later.

## Acceptance criteria

- `POST/GET/PUT/DELETE /api/v1/workflows` return documented, typed success and RFC 9457 problem responses, with validation/ownership errors that do not leak internals.
- Saving a workflow creates or updates a version according to the explicitly documented lifecycle; reads return the canonical persisted definition and version metadata.
- Activation/deactivation is idempotent and activation validates a compiled graph rather than trusting client state.
- `POST /api/v1/workflows/:id/run` creates a persisted execution request only after the workflow passes run validation; the final runner is introduced by its dependent ticket.
- Integration tests exercise create → retrieve → update/version → deactivate/delete, including invalid draft vs invalid activation/run behaviour.
- The generated OpenAPI document exposes the workflow surface used by the web client.

## References

- PRD: §§3.1, 18–20, 36 Workflow/Lifecycle API, 55–56.
- Design reference: `design-refs/n8n/01-workflows-list.png`, `26-publish-version-dialog.png` and INDEX entries 01/26.

## Relevant documentation

- Existing Huma/Chi API conventions in `internal/api/`. Use `find-docs` before relying on current Huma operation, response, validation, or OpenAPI APIs; record the official docs used.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `verification-before-completion`.

## Work Evidence

Closed by `pine close --evidence` on 2026-08-30.

- Base: _(none — ticket predates git history or creation time unknown; showing uncommitted changes only)_
- Commits (3):
  - `13d9d098` — feat(workflows): add node registry and lifecycle API
  - `f1c3c6fd` — chore(pine): mark active P1 tickets in progress
  - `6a9f56bb` — chore: initialize governance and Pine tracking
- Files changed (base → working tree):

```
 .pine/tickets/FEAT-6yef51.md | 14 ++++++++++++--
 1 file changed, 12 insertions(+), 2 deletions(-)
```
