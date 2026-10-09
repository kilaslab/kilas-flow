---
id: FEAT-e898nz
title: 'Workflow tags: a stored, listed field that n8n import and export round-trip'
status: doing
priority: low
created: "2026-10-08T16:58:07Z"
updated: "2026-10-09T11:03:29Z"
---

# Description

A workflow carries an ordered list of short labels (tags). Tags group workflows in the dashboard, the CLI, MCP and the API, and n8n import and export carry them, which closes BUG-6gkd12's "tags round-trip".

- Tags are an optional `tags` field of the canonical document (`workflow.Document.Tags`, `schemas/workflow-v1.schema.json`, OpenAPI `Document` and `WorkflowDocumentInput`). Each revision has its own tags, the way it has its own name, so a restore brings its tags back.
- Every path that appends a revision rewrites a per-tenant `workflow_tags` index in the same transaction. The list reads tags and filters by tag from that index without loading documents (BUG-fv5fer is why the list never loads them).
- They live in the document rather than in a repository-only column so that n8n import and export stay inside `internal/interop/n8n`, validation has one authority (`workflow.ValidateDraft`), no API operation is added, and the get, edit, update loop agents use already carries the field.

# Acceptance Criteria
- [ ] 1. The document has an optional `tags: string[]` (Go, JSON schema, OpenAPI). A document without tags encodes byte for byte as before (`omitempty`).
- [ ] 2. `ValidateDraft` enforces at most 20 tags; each tag 1 to 24 characters with no leading or trailing whitespace, no comma and no control character; tags unique ignoring case; authored order and case kept. A bad tag gets a 422 that names it. The 24-character limit is n8n's tag-name limit, so an export never carries a tag n8n refuses.
- [ ] 3. Save, restore and duplicate carry tags. `GET /workflows` returns `tags` on every summary, always an array (`[]` when none). `?tag=` filters case-insensitively and pages with the existing cursor. Deleting a workflow removes its tag rows.
- [ ] 4. Tenant scoping: one tenant's tags never list or filter in another tenant. `workflow_tags` is covered by the tenant purge and named on operate/tenant-deletion.md.
- [ ] 5. n8n import reads `tags` (objects with `name`) and keeps the names in order. Instance-local `id`, `createdAt` and `updatedAt` are ignored without a report. Everything it cannot keep is reported with `field: "tags"` and the file is never refused for it: a bare string, a missing or empty name, too long, a comma or control character, a case-insensitive duplicate, more than 20, or a `tags` value that is not an array. Pasting a full export through convert reports nothing about tags.
- [ ] 6. n8n export writes `"tags":[{"name":...}]` in document order. It invents no ids and leaves the key out when there are no tags. Import, export, import keeps the names.
- [ ] 7. `kilasflow workflow list --tag <name>` filters, and the MCP tool derived from the verb gets the same argument. JSON output includes `tags`.
- [ ] 8. Dashboard: each row shows its tags as chips (up to 3, then "+N"); the filter bar has a Tag select (`?tag=`); the Rename dialog edits tags (comma-separated); editor saves (dashboard and embed), rename and duplicate never drop tags.
- [ ] 9. Generated artefacts are regenerated and their `-check` targets pass. Docs are updated, and the SDK CHANGELOG has an Additive entry.

# Implementation Plan

See the Progress section: the plan was written by a planner (scratchpad plans/E-tags.md) and followed, with the deviations listed there.

# Notes

- n8n wire format: see the Progress section for what was checked and how.

# Related Files

- `internal/workflow/tags.go`, `internal/workflow/document.go`
- `migrations/{sqlite,postgres}/000025_workflow_tags.{up,down}.sql`
- `internal/repository/workflow_tags.go`, `workflows.go`, `workflow_history.go`, `tenant_rows.go`
- `internal/api/handlers/workflows.go`
- `internal/interop/n8n/tags.go`
- `internal/cli/verbs_workflow.go`
- `web/src/lib/dashboard/workflow-list.ts`, `web/src/lib/components/dashboard/workflow-tag-chips.svelte`, `web/src/routes/(dashboard)/app/workflows/+page.svelte`

# Attachments
