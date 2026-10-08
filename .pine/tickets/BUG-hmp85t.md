---
id: BUG-hmp85t
title: Revision preview can show a blank canvas; save/navigation messages expose revision ids, 'Try again' on 404, native confirm
status: done
priority: low
labels:
    - editor
    - versions
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:48:15Z"
updated: "2026-10-08T16:25:00Z"
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
- [x] The view is fitted when a revision preview starts and when it ends
- [x] A conflict banner without raw ids; 404 offers "Back to workflows"; unsaved changes use a Save/Discard/Cancel modal instead of the native confirm

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-f9frth

# Fix

- Preview fit (UXE-22): `workflow-editor.svelte` fits the view whenever `preview` starts, switches revision or ends (a plain `fittedPreview` variable, so the effect never tracks its own write). The history Sheet is an overlay, so the fit pads its right side by the panel's measured overlap of the canvas (`fitPaddingBeside`, `maxZoom: 1`); the Sheet carries `data-history-panel` for that measurement.
- Conflict (UXE-26.1): the "Save failed: 409 … expected revision wfv_…, latest is wfv_…" line is no longer printed under the conflict banner (`shownSaveError`); the conflict banner with Reload theirs / Save mine anyway is the one message.
- 404 (UXE-26.2): a workflow the API answers 404 for shows "This workflow does not exist" with a "Back to workflows" button and no "Try again"; other load failures keep Try again and gain a "Back to workflows" link.
- Unsaved changes (UXE-26.3): the native `confirm` is gone. An in-app navigation with a dirty canvas is cancelled and opens a dialog with Cancel / Leave without saving / Save and leave. Save and leave calls the editor's new `saveDraft()` export and leaves only if the canvas is clean afterwards; a failed or conflicting save closes the dialog and leaves the user on the canvas with the banner that says why. Reloads, tab closes and external links (`willUnload`) still use the browser's beforeunload prompt. `workflows_unsaved_confirm` replaced by nine new keys in en and id.
- Helpers and tests: `web/src/lib/workflow-editor/save-feedback.ts` (+ `.test.ts`, 8 tests: conflict detail suppressed, 404 detection on ApiError, padding beside the panel). `pnpm check` 0 errors; `pnpm vitest run src/lib/workflow-editor src/lib/dashboard src/lib/components src/lib/api src/routes` 690 pass.
- Playwright (real build, 1280x800): a workflow whose revision 1 holds one node at (2600,1400) and revisions 2-3 four nodes near the origin; after panning the draft away, clicking Revision 1 showed "Previewing revision 1…" with the node fitted at x 634, left of the open panel (blank before the fix); Back to draft fitted all four draft nodes in view. Opening wf_00000000-… showed "This workflow does not exist", one "Back to workflows" link (which lands on /app/workflows) and no Try again. A save after another writer's PUT showed exactly one alert ("This workflow changed elsewhere while you were editing. Reload theirs Save mine anyway"), and the page text contained no `wfv_`. With a dirty canvas, clicking Executions opened the dialog (Cancel / Leave without saving / Save and leave): Cancel stayed on the editor, Leave without saving went to /executions with the revision unchanged, Save and leave went to /executions with the revision going 4 -> 5.
- Not changed, noticed while testing: the preview banner's own "Back to draft" button sits under the open history panel at 1280 px (the panel has its own Back to draft, which works).

# Related Files

- web/src/lib/workflow-editor/save-feedback.ts
- web/src/lib/components/workflow-editor/workflow-editor.svelte
- web/src/lib/components/workflow-editor/version-panel.svelte
- web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte
- web/messages/en/workflows.json
- web/messages/id/workflows.json

# Attachments
