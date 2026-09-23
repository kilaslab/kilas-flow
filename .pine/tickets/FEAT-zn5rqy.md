---
id: FEAT-zn5rqy
title: 'Executions UI: retry (original/current), delete, debug-in-editor, date filter, per-workflow link, sticky actions'
status: todo
priority: medium
labels:
    - executions
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

The API can already retry an execution, but the UI never calls it. Delete, "Debug in editor" and a date filter are missing, and the editor's "Open executions" link is unfiltered.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-16, UXD-24). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## UXD-16: The executions UI has no Retry, Delete, "Debug in editor", annotations or time filter, and the editor's "Open executions" is unfiltered

*gap · medium · executions*

**n8n:** Failed executions offer "Retry with currently saved workflow" and "Retry with original workflow", and the retry is linked to its parent. There is delete and bulk delete, "Debug in editor" (copies the run into the canvas as pinned data), annotations and tags with a rating, filters by status, workflow, start date and tags, and a per-workflow Executions tab.

**Steps to reproduce:**

1. Open `/executions` and any failed execution detail. 2. `kilasflow exec retry exec_01a0cbce-25d8-707d-8e7b-12a8dec7d7b4`. 3. In an editor, click the Activity (Open executions) button.

**Actual:**

The detail page offers only Stop (while running) and Copy error, and the list offers Status and Workflow filters and Auto-refresh. There is no Retry although the API has `POST /executions/{id}/retry` (the generated client has `retryExecution`, unused by the UI), no DELETE endpoint, no annotation or date filter, and no Debug in editor. The CLI retry re-runs the original revision only; the new execution has `"trigger":"manual"` and no reference to the execution it retries. The editor button goes to `/executions` without `?workflowId=`, while the list page's comment says it does.

**Expected:**

Retry buttons (original or current revision), delete, a retryOf link, a date filter, and a link to the workflow's own executions.

**Suggested fix:**

Add UI actions for the existing retry API with a `useCurrentRevision` flag and store `retryOf`. Add DELETE /executions/{id}, and link with `?workflowId=`.

**Evidence:**

`$SP/agents/ux-debug/exec-list-01.png`, `a-05-exec-detail.png`, `cli/retry_f.json`. web/src/lib/components/workflow-editor/workflow-editor.svelte:1042 (`href="/executions"`), web/src/routes/(dashboard)/executions/+page.svelte:91.

**Related:**

BUG-6bqh51 (done; "Executions have no Retry, no Debug in editor, no delete" and "the editor's Open executions opens the unfiltered list" still reproduce in the UI)


## UXD-24: The executions table's Stop control is scrolled out of view on a 1600 px wide window

*ux · low · executions*

**n8n:** The row actions are always visible.

**Steps to reproduce:**

1. Viewport 1600×1000 with the sidebar expanded. 2. Start `[ux-debug] s slow chain` and open `/executions`.

**Actual:**

The table (1089 px) overflows its 1022 px container (`overflow-x:auto`). The running row's Stop control is clipped to `☐ St` at the right edge, and reaching it needs a horizontal scroll.

**Expected:**

The actions column is visible (sticky, or with the other columns narrowed).

**Suggested fix:**

Make the last column sticky, or truncate the execution-id column.

**Evidence:**

`$SP/agents/ux-debug/exec-list-01.png`, `exec-list-02-running-row.png`.

**Related:**

none
---


# Acceptance Criteria
- [ ] Retry buttons for the original and the current revision, with `retryOf` stored and shown
- [ ] `DELETE /executions/{id}`, plus bulk delete in the list
- [ ] "Debug in editor" loads the run's data into the canvas as pinned data (depends on the pinned-data ticket)
- [ ] A start-date filter; the editor links to `/executions?workflowId=…`
- [ ] The row actions column stays visible (sticky) at 1280–1600 px widths
- [ ] The whole row, or the workflow name, opens the execution with keyboard support; the id becomes secondary text and no longer requires horizontal scroll at 1024 px (audit finding OPS-24)

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-6bqh51

# Related Files

# Attachments
