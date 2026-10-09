---
id: FEAT-vntngh
title: Google Sheets node, trigger and tool (the most-used n8n app node)
status: doing
priority: critical
labels:
    - n8n
    - parity
    - node-catalog
    - google
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-10-09T11:03:29Z"
---

# Description

Google Sheets appears in 33.5% of the 998 sampled n8n templates (45.7% of the newest). It is the #1 app node, and after Code it unlocks the most templates. KilasFlow has no Sheets node, and because HTTP Request cannot attach an OAuth2 credential, there is no workaround either.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** `n8n-nodes-base.googleSheets` appears in 334/998 templates (33.5%; 45.7% of the newest), #6 overall and #1 among app nodes. The Sheets family (node, Trigger, Tool) together appears in 35.0% of templates (48.1% of the newest). It is #1 on n8n.io/integrations and #7 on n8n Pulse.

# Steps to Reproduce

1. Import Manual → Google Sheets v4.7. I used a real instance from template 19200. Correction (2026-10-09): 19200 has no `appendOrUpdate` node. Its 8 Sheets nodes are 2 × read (no `operation`, so n8n's default `read`), 3 × append (`columns` defineBelow) and 3 × update (`matchingColumns: ["row_number"]`), with `documentId`/`sheetName` locators and the `columns` resourceMapper. `appendOrUpdate` is still an n8n operation we implement; it just is not in this template.
2. Run it.
3. Repeat with `googleSheetsTrigger` and `googleSheetsTool`.

# Expected

Sheets read / append / appendOrUpdate / update / delete / clear, plus the trigger (events `rowAdded`, `rowUpdate`, `anyUpdate`; n8n spells the update event `rowUpdate`, not `rowUpdated`) and the tool variant, import and run.

# Actual

- Import issue: blocking "KilasFlow has no equivalent of the n8n node \"n8n-nodes-base.googleSheets\". It was imported as an unsupported placeholder: the workflow can be edited, but it cannot run until this node is replaced." The parameters survive only inside the `original` capsule.
- The run fails with 422: "this node was imported from n8n-nodes-base.googleSheets, which KilasFlow does not support."
- There is also no workaround. `GET /api/v1/credential-types` has no Sheets or generic OAuth2 type, and the HTTP Request node cannot attach an OAuth2 credential (see NG-19).

# Acceptance Criteria
- [ ] `n8n-nodes-base.googleSheets` imports onto a native `kilasflow.googleSheets` that implements read/append/appendOrUpdate/update/delete/clear, using n8n's operation and value strings and the `columns` resourceMapper
- [ ] `googleSheetsTrigger` (rowAdded/rowUpdate/anyUpdate) runs on the leased poll framework, and `googleSheetsTool` is usable by the AI Agent
- [ ] Uses the existing Google OAuth2 credential flow from Drive/Gmail
- [ ] Template 19200 imports with no blocking issue and runs its Sheets step against a stub or real sheet

# Implementation Plan

Build on the Google OAuth2 (FEAT-mc6s65) and leased poll trigger (FEAT-wcmk3r) that Drive and Gmail already use. Ship `kilasflow.googleSheets` with n8n's operation and value strings, the resourceMapper `columns`, a Sheets trigger and a tool variant, and add the three importer mappings.

# Notes

Related tickets: FEAT-gcq50s, FEAT-mc6s65, FEAT-wcmk3r

Related (from the audit): FEAT-gcq50s explicitly marks Google Sheets "out of scope". No open ticket covers it.

## Scope split (2026-10-09)

- Wave 1 (this branch): node, trigger, the two credential types, option/schema loaders, importer mappings, a hand-authored 19200-shaped fixture and an end-to-end run test.
- Wave 2: `googleSheetsTool`, after the generic base-executor-as-tool mechanism from the AI-tools track lands. Acceptance criterion 2 stays half open until then.

## Behaviour notes (written in our own words, 2026-10-09)

Wire-format and behaviour facts only, read as a behaviour reference under memory/licensing.md (2026-09-24). Nothing here is copied from n8n.

Node `n8n-nodes-base.googleSheets` (published versions 3, 4, 4.1 to 4.7; we write 4.7 and read 4 to 4.7, and refuse the v3 shape):
- `authentication` is `oAuth2` (default) or `serviceAccount`. The credential is `googleSheetsOAuth2Api`, or `googleApi` for a service account.
- `resource` is `sheet` (default) or `spreadsheet`. Sheet operations: `read` (default), `append`, `appendOrUpdate`, `update`, `delete`, `clear`, `create`, `remove`. We run the six data operations and refuse `create`, `remove` and the `spreadsheet` resource at import.
- `documentId` is a resource locator with modes `list`, `url` and `id`. `sheetName` has `list`, `url`, `id` (a number or `gid=N`) and `name`. In `list` mode the sheet value is the numeric gid.
- From v4 the `columns` parameter is a resourceMapper value: `{mappingMode: defineBelow | autoMapInputData, value, matchingColumns, schema, attemptToConvertTypes, convertFieldsToString}`. Only one matching column is allowed. For `update`, the pseudo column `row_number` can be the match.
- Read: `filtersUI.values[]{lookupColumn, lookupValue}` with `combineFilters` AND or OR. The default is OR below 4.3 and AND from 4.3. Below 4.5 the option is `returnAllMatches` with the strings `returnFirstMatch` (default) or `returnAllMatches`; from 4.5 it is a boolean `returnFirstMatch`, default false. `options.dataLocationOnSheet.values` carries `rangeDefinition`, `readRowsUntil`, `headerRow` (1), `firstDataRow` (2) and `range`. `options.outputFormatting.values` carries `general` (UNFORMATTED_VALUE, FORMATTED_VALUE, FORMULA) and `date` (FORMATTED_STRING, SERIAL_NUMBER).
- Read output: one item per data row, keyed by the header row, plus `row_number` (the real sheet row). Missing cells are the empty string.
- Write options: `cellFormat` USER_ENTERED (default) or RAW, `locationDefine.values{headerRow, firstDataRow}`, `handlingExtraData` insertInNewColumn (default), ignoreIt or error, `useAppend` bool.
- `delete`: `toDelete` is rows or columns. `startIndex` is a 1-based row number (default 2) for rows and a column letter (default "A") for columns. `numberToDelete` defaults to 1. It emits one `{success: true}` item.
- `clear`: `clear` is wholeSheet, specificRows, specificColumns or specificRange, plus `keepFirstRow`. `range` defaults to "A:F". It passes its input items through.
- `alwaysOutputData` (a node setting) matters here because a read with no match returns zero items.

Node `n8n-nodes-base.googleSheetsTrigger` (version 1):
- `authentication` is `triggerOAuth2` (default) or `serviceAccount`; credential `googleSheetsTriggerOAuth2Api`.
- `event` is `rowAdded`, `rowUpdate` or `anyUpdate` (default). `includeInOutput` is new (default), old or both. `options`: `columnsToWatch`, `dataLocationOnSheet`, `valueRender`, `dateTimeRenderOption`. `pollTimes` as the other poll triggers.
- The first non-manual poll stores a baseline and emits nothing.

`googleSheetsTool` exports as the base parameters plus `descriptionType` and `toolDescription` (wave 2).

Open to confirm before closing: the `update`/`appendOrUpdate` match rule (first row whose matching column text equals the input value) and the output shapes of the write operations. Recorded under Progress once checked.

# Related Files

- `probes/Google_Sheets.json`
- `results-extra.json` (Google Sheets Trigger)
- `results-ai.json` (Google Sheets Tool)
- `greedy-unlock.json`: Sheets is the #2 unlock (+85 templates).
- `internal/interop/n8n/n8n.go:803-816`

# Attachments
