---
id: BUG-hmp85t
title: Revision preview can show a blank canvas; save/navigation messages expose revision ids, 'Try again' on 404, native confirm
status: todo
priority: low
labels:
    - editor
    - versions
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:48:15Z"
updated: "2026-09-23T01:48:15Z"
---

# Description

Small rough edges in version history and in save conflicts.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-22, UXE-26). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## UXE-22: Previewing an older revision can show a blank canvas, because the view is not fitted to that revision

*bug · low · version history*

**n8n:** Opening a version in history shows that version fitted to view.

**Steps to reproduce:**

1. In a workflow saved several times, open History and click "Revision 2".

**Actual:**

The banner reads "Previewing revision 2", but the canvas is empty. The revision's only node is under the history panel, at the viewport kept from the current draft. It appears only after pressing Fit View. Restore itself works well (a confirmation with a reason, saved as a new revision).

**Expected:**

fitView when a preview starts and when it ends.

**Suggested fix:**

Call `flow.fitView({padding, maxZoom: 1})` when `preview` changes, offset for the panel width.

**Evidence:**

SD/48-revision-preview.png, SD/48b-revision-preview-fit.png, SD/49-restored.png.

**Related:**

none


## UXE-26: Save and navigation messages expose internals: raw revision ids on conflict, "Try again" for a 404, and a native confirm for unsaved changes

*ux · low · saving / error states*

**n8n:** Conflicts and "unsaved changes" use styled modals with Save / Leave without saving / Cancel. A missing workflow page offers a way back.

**Steps to reproduce:**

1. Edit a workflow in two tabs and save both. 2. Open /app/workflows/wf_00000000-0000-0000-0000-000000000000. 3. Make an edit and click "Executions" in the sidebar.

**Actual:**

(1) Two stacked banners: "This workflow changed elsewhere…" [Reload theirs][Save mine anyway], plus `Save failed: 409 — This workflow changed since you loaded it (expected revision wfv_01a0cbd9-…, latest is wfv_01a0cbda-…). Reload and save again.` Detection and both actions work. (2) "Workflow editor could not be loaded · 404 — workflow not found · [Try again]", with no link back. (3) A native `confirm("This workflow has unsaved changes. Leave and discard them?")` with no "Save and leave".

**Expected:**

One banner without ids, a "Back to workflows" action on 404, and a Save/Discard/Cancel modal.

**Suggested fix:**

Drop the second 409 banner and the ids. Add a back link on 404. Replace the native confirm with a dialog that can save first.

**Evidence:**

SD/52-save-conflict.png, SD/65-missing-workflow.png, SD/guard2.js.

**Related:**

BUG-f9frth (done; save conflicts and unsaved guard)


# Acceptance Criteria
- [ ] The view is fitted when a revision preview starts and when it ends
- [ ] A conflict banner without raw ids; 404 offers "Back to workflows"; unsaved changes use a Save/Discard/Cancel modal instead of the native confirm

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-f9frth

# Related Files

# Attachments
