---
id: FEAT-s99vdp
title: 'Import report and placeholder UX: readable/grouped/clickable report; placeholder & Code panels explain and suggest'
status: todo
priority: medium
labels:
    - importer
    - ux
    - ndv
parent: EPIC-8rbys7
created: "2026-09-23T01:35:50Z"
updated: "2026-09-23T01:35:50Z"
---

# Description

The import report is KilasFlow's own addition on top of n8n, and a good one, but its reasons sit off-screen and its rows can't be clicked. Placeholder nodes show an editable raw capsule, and the Code node's suggested replacement is never shown.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: n8n-templates; finding ids: TPL-8, TPL-9, TPL-10). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## TPL-8: The import report is hard to read: the reason column is off-screen, the dialog is clipped, and rows cannot be clicked or grouped

*ux · medium · importer*

**n8n:** Not applicable. n8n has no import report; it opens the workflow and marks nodes with issues. The KilasFlow report is a useful addition, which makes its layout matter.

**Steps to reproduce:**

1. Open imported 1934. Click "Import report · 17".
2. On the Workflows page, click "Import n8n", paste `templates/2462.json` (or upload `templates/1961.json`) and click Import workflow.

**Actual:**

- **Editor drawer.** It is 384 px wide and holds a 755 px table, so the "What happened" column, the only one that explains anything, is off-screen until you scroll sideways. The alert is clipped ("…9 blocking issues are f"). The canvas behind is blurred.
- **Post-import dialog.** Content is 855 px wide inside a 768 px dialog. Autofocus lands on "Copy URL" and scrolls the body 87 px sideways, so the left edge of the title, the alert, the headings and the severity badges is cut off ("le became…", "dresses · 1").
- **Rows.** Node names are not links (row cursor `auto`; clicking "Chat_mode" does nothing), even though the alert says "Each one names the node to open below".
- **Repetition.** Identical rows are not grouped: 5 identical telegramApi credential paragraphs in 1934, and 15 rows in 2462.
- **Source type.** Translator and connection issues show "v0" (for example "CheckCommand · v0", "Workflow · v0") because the issue carries no type.
- A wrapped template body (`{"id":…,"workflow":{…}}`) is refused with "422 — the workflow contains no nodes".

**Expected:**

- The reason text is visible without horizontal scrolling. A stacked card layout at ≤ 768 px would do it.
- Rows are grouped by cause, for example "5 Telegram nodes need a telegramApi credential [Attach]".
- Each node name selects and zooms to the node.
- Issues always carry the node's source type.

**Suggested fix:**

Render the diagnostics as a responsive list (reason under node name). Stop autofocusing the Copy button, or set `overflow-x: hidden` on the dialog grid. Group by (severity, field, reason template). Make the node cell a button that focuses the node. Stamp `Type`/`TypeVersion` on translator and connection issues in `withDefaultSeverity`.

**Evidence:**

- `ui_1934_import_report.png`, `ui_1934_import_report_scrolled.png`, `ui_import_paste_result.png`, `ui_import_upload_result.png`, `ui_import_wrapper.png`
- DOM measurements: drawer 384 px with table 754 px; dialog `scrollWidth` 855 vs `clientWidth` 768, `scrollLeft` 87.
- Components under `web/src/lib/components/` using `web/src/lib/workflow-editor/import-diagnostics.ts`.

**Related:**

FEAT-0556ck (done, "Put n8n import and export in the editor"). OPS-14 covers the "422 —" prefix in general.


## TPL-9: The Code placeholder's "Suggested replacement" shows a generic description, never the importer's suggestion

*bug · medium · ndv*

**n8n:** Not applicable (n8n runs the code). FEAT-8qyfh1 promised that an imported Code node "fails compilation with a message naming the node and the alternative" and shows the source.

**Steps to reproduce:**

1. Import template 1747 "Joining different datasets" (or 3121, whose node's suggestion is "A Filter node does this without code.").
2. Click the "A. Ingredients Needed" Code node.
3. Read "Suggested replacement".

**Actual:**

- The notice reads "What this server suggests instead, worked out from the source when it recognised a common shape." That is the property's description, not the suggestion.
- The node's stored `parameters.replacement` ("Replace it with the native nodes that do the same work — Filter, Switch, Set, … — or rewrite it in the Go Code node.", or node-specific text such as "A Set node with expressions does this without code.") is never shown.

**Expected:**

The notice shows the node's own `replacement` value.

**Suggested fix:**

For `notice` properties, render the stored value when there is one and fall back to the description. Alternatively, make `replacement` a read-only string property.

**Evidence:**

- `ui_1747_ndv_foreigncode.png`
- `web/src/lib/components/workflow-editor/property-field.svelte:633-639` renders `property.description || property.label` for every `notice`.
- `nodes/jscode.go:70` (`replacement` is a notice property).
- `internal/interop/n8n/parameters.go:3184` (the importer stores it).

**Related:**

FEAT-8qyfh1 (done). Its promise of a message naming the alternative is only met in the import report, not in the node view.


## TPL-10: The unsupported node's panel shows an editable raw capsule and doesn't say what is missing or what to do

*ux · medium · ndv*

**n8n:** A missing node type opens a panel that says the node isn't installed and what to install. Its parameters are preserved and not exposed as raw JSON.

**Steps to reproduce:**

1. Open imported 1954.
2. Click the SerpAPI placeholder.

**Actual:**

- The Parameters tab shows "Original node type", "Original type version", and "Original definition": a key/value editor over the capsule (credentials, id, name, parameters, position, type, typeVersion), each with a delete (×) button and an "Add field" button.
- Nothing explains why the node cannot run or what could replace it. The only explanation is a footer truncated to "An imported node KilasFlow has n…".
- Editing or deleting capsule keys (for example `type`) changes what export restores.

**Expected:**

A read-only summary, for example "n8n node *SerpAPI tool* (`@n8n/n8n-nodes-langchain.toolSerpApi` v1) is not available in KilasFlow", with a suggested native replacement (HTTP Request Tool against serpapi.com), a "Replace node" action, and the original parameters read-only.

**Suggested fix:**

Give `kilasflow.unsupported` a dedicated panel: read-only capsule, reason text, and a per-type replacement hint table shared with the importer.

**Evidence:**

`ui_1954_ndv_unsupported.png`, `nodes/unsupported.go` (capsule exposed as editable parameters)

**Related:**

NG-18 in `findings/node-gap.md` asks for a replacement-hint table in `placeholderFor`. This finding covers the panel side.


# Acceptance Criteria
- [ ] The report uses a responsive layout with the reason visible without horizontal scroll, and the post-import dialog doesn't autofocus-scroll
- [ ] Identical issues are grouped ("5 Telegram nodes need a telegramApi credential [Attach]")
- [ ] Node names in the report select and zoom to the node
- [ ] Every translator and connection issue carries the source type and version (no "v0")
- [ ] The Code placeholder panel shows the node's stored `replacement` text
- [ ] The unsupported-node panel is a read-only summary: the original type and version, why it can't run, a replacement hint (a table shared with the importer), and a "Replace node" action. The capsule can't be edited
- [ ] A wrapped template body (`{"workflow":{…}}`) is accepted by the UI import

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-0556ck, FEAT-8qyfh1

# Related Files

# Attachments
