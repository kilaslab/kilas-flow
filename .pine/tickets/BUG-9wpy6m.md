---
id: BUG-9wpy6m
title: The Google Sheets tool accepts a template string for its whole columns, and its run-time check skips the generic structural rule
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-16T15:30:38Z"
updated: "2026-10-16T15:30:38Z"
---

# Description

Follow-up from the 2026-10-16 security re-review of the Google Sheets agent tool (FEAT-vntngh). Two low-severity gaps:

- L1: `checkSheetsToolColumns` (nodes/google_sheets_tool.go) returns nil when `columns` is not a map, so a whole `columns` written as a plain template string (`"{{ $json.cols }}"`) passes tool validation; `resolveGoogleIR` then turns it into an expression and the agent's input item chooses the columns written (still RAW). The filters check already refuses the same string shape.
- L2: `refuseSheetsToolRun` re-runs the form's own validator before any Google call but not the generic structural-key rule (documentId, sheetName, options, combineFilters, numberToDelete); those are only checked at validation and tool build.

# Acceptance Criteria
- [ ] A tool form's `columns` that is not a plain map (string, marker, list) is refused at validation and before any Google call, with a test.
- [ ] `refuseSheetsToolRun` also applies the generic structural-key rule, with a test.
