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
- [ ] An inline editable title
- [ ] A Workflow settings dialog with execution timeout (seconds, or none), error workflow, timezone and save-execution options, written to `document.settings`

# Implementation Plan

Make the header title an inline-edit control that reuses the list's rename call. Add a settings dialog.

# Notes

Related tickets: FEAT-jvembs

Related (from the audit): FEAT-jvembs ("Workflows can't be renamed, and workflow settings … have no UI", listed as Not done)

# Related Files

SD/03-new-canvas.png. routes/(dashboard)/app/workflows/[id]/+page.svelte:495.

# Attachments
