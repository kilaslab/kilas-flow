---
id: FEAT-eqzpzq
title: 'NDV and inspector data panes: Input/Output with Table/JSON/Schema, search, paging, binary preview'
status: todo
priority: high
labels:
    - editor
    - ndv
    - debugging
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

Inspecting data is the core of n8n debugging. KilasFlow shows only raw `<pre>` JSON, including engine plumbing (`pairedItem`), and a binary file cannot be viewed or downloaded at all. FEAT-56nep4 is marked done with this item unchecked.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-2, UXD-21). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## UXD-2: No input or output panes in the node view, and the execution inspector shows only raw JSON with internal lineage fields

*gap · high · ndv*

**n8n:** The NDV has INPUT, parameters and OUTPUT panes with Schema, Table and JSON views, search/filter, item paging, paired-item hover highlighting between input and output, a run selector, and binary View/Download. You can also drag a field into a parameter.

**Steps to reproduce:**

1. In any workflow, click a node: only Parameters and Settings are shown. 2. Open any execution, for example `/executions/exec_01a0cbc5-4638-78ca-ae6b-1e3409fbfe4a`, and click `Call API`.

**Actual:**

The execution inspector is a 22rem side panel with `Error`, `Input (238 chars)` and `Output (4 chars)` as pretty-printed `<pre>` JSON. The JSON includes engine plumbing on every item (`"pairedItem": {"itemIndex":0,"lost":true,"runIndex":0,"sourceNodeId":"five-items"}`). There is no Table or Schema view, no search, no item paging, no lineage highlighting and no copy-a-path. The editor's node panel has no data at all.

**Expected:**

n8n-style input and output panes with Table, JSON and Schema views, search, paired-item highlighting, and the same panes available in the editor for the last run.

**Suggested fix:**

Build a shared data-pane component with Table, JSON and Schema views, search and item paging, and hide `pairedItem` behind a toggle. Use it in both the execution inspector and the editor's node panel.

**Evidence:**

`$SP/agents/ux-debug/a-03-ndv.png`, `a-04-node-click.png`, `a-06-exec-node.png`, `h-01-continue-error-output.png`. web/src/routes/(dashboard)/executions/[id]/+page.svelte:355-378 (`<pre>{asJSON(selectedRun.input/output)}</pre>`).

**Related:**

FEAT-56nep4 (status done; acceptance item "The NDV has no INPUT/OUTPUT data panes, Execute step, pinned data…" is unchecked and still reproduces)


## UXD-21: Binary data in an execution can't be viewed or downloaded

*gap · medium · executions*

**n8n:** The Binary tab shows a preview for images, PDFs, text and JSON, with View and Download buttons and the file metadata.

**Steps to reproduce:**

1. Run `[ux-debug] bin binary download` (HTTP → `/png`, response format File). 2. Open the execution and click `Get PNG`.

**Actual:**

The inspector lists `png · image/png · 70 B · item 1` and says `Contents are held by the server and are not shown here.`. The API has no binary read endpoint, so the file is unreachable from both the UI and the CLI. The stored file name is `png`, taken from the path with no extension.

**Expected:**

An authenticated download/preview endpoint (`GET /executions/{id}/binary/{binaryId}`) and View/Download in the inspector.

**Suggested fix:**

Add a tenant-scoped binary read endpoint with a content-type allow-list for inline preview, and show image and text previews.

**Evidence:**

`$SP/agents/ux-debug/bin-01-binary-inspector.png`, `cli/run_bin.json`. web/src/routes/(dashboard)/executions/[id]/+page.svelte:94-98.

**Related:**

FEAT-0f87fn (done, binary store)


# Also found by the audit

## UXE-13: No input panel, so fields cannot be dragged into parameters, and no expression result appears even after a run

*gap · high · ndv / expressions*

**n8n:** The NDV has INPUT and OUTPUT panes. Dragging a field from INPUT into a parameter inserts `{{ $json.field }}`, and every expression shows a live "Result" preview for the current item.

**Steps to reproduce:**

1. Run "[ux-editor] First flow" successfully ("Run succeeded."). 2. Open HTTP Request and switch URL to expression mode. 3. Look for input data to drag, and for a result preview.

**Actual:**

The inspector is a 320 px side panel with only Parameters and Settings tabs. There is no input or output data and nothing to drag. The expression textarea shows only "Resolved per item on the server, for example {{ $json.id }}." No result preview appears after the run. Completions do work (`$j` → `$json`, `$jmespath()`).

**Expected:**

n8n's three-pane NDV with drag-and-drop mapping and a result preview driven by the last run.

**Suggested fix:**

See UXD-2/UXD-10. The drag half can land first: a read-only INPUT pane fed by the last execution's upstream output, with draggable keys that insert `{{ $json.path }}` into the focused field.

**Evidence:**

SD/30-http-ndv-after-run.png, SD/31-url-expression-mode.png, SD/32-expr-autocomplete.png.

**Related:**

FEAT-56nep4 (done with "NDV has no INPUT/OUTPUT data panes … drag-and-drop mapping" unchecked), UXD-2, UXD-10


# Acceptance Criteria
- [ ] A shared data-pane component with Table, JSON and Schema views, search and item paging, used by both the editor NDV and the execution inspector
- [ ] `pairedItem` is hidden behind a toggle, and paired-item hover highlighting links the input and output items
- [ ] A field can be dragged from the input pane into a parameter to insert its expression
- [ ] An authenticated, tenant-scoped `GET /executions/{id}/binary/{binaryId}` endpoint, plus View/Download and inline preview for images, PDF, text and JSON
- [ ] The NDV is the n8n three-pane layout (Input | Parameters | Output) in the editor, fed by the last run, and dragging an input field into a parameter inserts its expression

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-0f87fn, FEAT-56nep4

# Related Files

# Attachments
