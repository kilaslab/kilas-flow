---
id: FEAT-fpqg78
title: Rename the workflow and edit workflow settings (timeout, error workflow, timezone) from inside the editor
status: todo
priority: low
labels:
    - editor
    - settings
parent: EPIC-8rbys7
created: "2026-09-23T01:48:15Z"
updated: "2026-09-23T01:48:15Z"
---

# Description

The workflow title isn't editable in the editor, and settings the engine already honours (`executionTimeout`, `errorWorkflow`, timezone) have no UI.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-21). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Click the workflow name in the editor header to rename it inline. "Workflow settings" (timezone, error workflow, save-data options) sits in the header menu.

# Steps to Reproduce

1. Open a workflow. 2. Click or double-click the name "[ux-editor] First flow" in the header.

# Expected

An inline editable title and a settings dialog.

# Actual

The name is a plain `<p>`, and rename exists only in the list's row menu (which works). There is no settings entry anywhere in the editor, and the "Open executions" icon links to the unfiltered /executions (UXD-16).

# Acceptance Criteria
- [x] An inline editable title
- [ ] A Workflow settings dialog with execution timeout (seconds, or none), error workflow, timezone and save-execution options, written to `document.settings`

# Implementation Plan

Make the header title an inline-edit control that reuses the list's rename call. Add a settings dialog.

# Notes

Related tickets: FEAT-jvembs

Related (from the audit): FEAT-jvembs ("Workflows can't be renamed, and workflow settings … have no UI", listed as Not done)

# Related Files

SD/03-new-canvas.png. routes/(dashboard)/app/workflows/[id]/+page.svelte:495.

# Attachments

## Progress

2026-11-27 (web only, commit 6351b03a and a test follow-up):

- Inline title: the editor header's workflow name (`workflow-title.svelte`) is a button that turns into an input on click. Enter or blur commits, Escape cancels, an empty or over-255-character name is refused with a message. It writes `draft.name`, so a rename is undoable, marks the canvas dirty and saves through the editor's existing save path (no new API call). A locked canvas (read-only, or previewing a revision) renders a plain name.
- Workflow settings dialog (`workflow-settings-dialog.svelte`, toolbar gear, dashboard header only): execution timeout in seconds (empty means the instance default, a "No time limit" box writes -1), error workflow (select of the tenant's other workflows via `listWorkflows`, never the workflow itself), timezone (select from `Intl.supportedValuesOf('timeZone')` plus UTC and the stored zone, empty means the server default). Keys written to `document.settings`: `executionTimeout`, `errorWorkflow`, `timezone`; empty fields remove their key and other keys are kept. Pure logic with tests is in `web/src/lib/workflow-editor/workflow-settings.ts`.
- All three keys are read by the engine: `engine.ExecutionTimeoutSetting` (`internal/engine/service.go` runBudget, negative means no timeout, positive is capped by the instance max), `errorWorkflowSetting` (service.go errorWorkflowID) and the timezone (`internal/workflow/compiler.go` validateDocumentTimezone, `engine/eval.go`, `scheduler`).
- Left out: save-execution options (`saveDataErrorExecution`, `saveDataSuccessExecution`, `saveManualExecutions`, `saveExecutionProgress`). The Go engine reads none of them (no match outside the n8n import), so offering them would write keys that do nothing. The second criterion therefore stays unticked until the engine honours any of them, or the criterion is narrowed.
- Not done: the embed surface (no header) shows neither the title nor the settings entry; the "Open executions" link still goes to the unfiltered /executions (UXD-16, separate finding).
