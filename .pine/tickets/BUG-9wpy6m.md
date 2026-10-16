---
id: BUG-9wpy6m
title: The Google Sheets tool accepts a template string for its whole columns, and its run-time check skips the generic structural rule
status: done
priority: medium
parent: EPIC-8rbys7
created: "2026-10-16T15:30:38Z"
updated: "2026-10-16T15:36:00Z"
---

# Description

Follow-up from the 2026-10-16 security re-review of the Google Sheets agent tool (FEAT-vntngh). Two low-severity gaps:

- L1: `checkSheetsToolColumns` (nodes/google_sheets_tool.go) returns nil when `columns` is not a map, so a whole `columns` written as a plain template string (`"{{ $json.cols }}"`) passes tool validation; `resolveGoogleIR` then turns it into an expression and the agent's input item chooses the columns written (still RAW). The filters check already refuses the same string shape.
- L2: `refuseSheetsToolRun` re-runs the form's own validator before any Google call but not the generic structural-key rule (documentId, sheetName, options, combineFilters, numberToDelete); those are only checked at validation and tool build.

# Acceptance Criteria
- [x] A tool form's `columns` that is not a plain map (string, marker, list) is refused at validation and before any Google call, with a test. Evidence: `TestTheGoogleSheetsToolRefusesAColumnsThatIsNotAPlainMap` (nodes/google_sheets_tool_followup_test.go) fails before the change and passes after; `checkSheetsToolColumns` now takes only a plain map, and the executor's `refuseSheetsToolRun` runs it before any request leaves.
- [x] `refuseSheetsToolRun` also applies the generic structural-key rule, with a test. Evidence: `TestTheExecutorAppliesTheAuthorOnlyKeysWhenRunAsATool` (documentId, sheetName, options, combineFilters, numberToDelete as expressions are refused naming the key, with no Google call); `refuseSheetsToolRun` now calls `checkToolFormParameters` for the Sheets form. `go test -count=1 ./nodes/... ./internal/interop/n8n/...` passes.

## Work Evidence

Closed by `pine close --evidence` on 2026-10-16.

- Base: `1ee0166f` (last commit at or before ticket created 2026-10-16)
- Commits (3):
  - `5fc781d4` — merge: the Google Sheets tool refuses a columns that is not a plain mapping, and its run-time check applies the generic author-only rule (BUG-9wpy6m)
  - `86f7cca5` — BUG-9wpy6m: the Sheets tool refuses a columns that is not a plain map, and its run-time check applies the author-only keys
  - `1ee0166f` — chore(pine): BUG-9wpy6m, BUG-745mh2 and BUG-1vh6k6 record three small follow-ups from tonight's reviews
- Files changed (base → working tree):

```
 .pine/tickets/BUG-1vh6k6.md                        |  43 ++-
 .pine/tickets/BUG-745mh2.md                        |   8 +-
 .pine/tickets/BUG-9wpy6m.md                        |   4 +-
 .pine/tickets/FEAT-53pa9a.md                       |  17 +-
 cmd/kilasflow/main.go                              |   3 +
 config.example.yaml                                |   9 +
 docs/src/content/docs/guides/node-authoring.md     |  20 +-
 .../docs/operate/configuration-reference.md        |  15 +
 docs/src/content/docs/reference/node-packs.md      |   4 +-
 internal/config/boot_strictness_test.go            |  33 ++
 internal/config/config.go                          |  22 +-
 internal/engine/service.go                         |  74 ++---
 internal/engine/terminal_log.go                    | 191 ++++++++++++
 internal/engine/terminal_log_test.go               | 334 +++++++++++++++++++++
 internal/engine/wait_service.go                    |   8 +-
 nodes/google_sheets_tool.go                        |  16 +-
 nodes/google_sheets_tool_followup_test.go          |  67 +++++
 .../workflow-editor/properties-panel.svelte        |  47 +--
 .../workflow-editor/properties-panel.test.ts       |  44 +++
 .../workflow-editor/workflow-editor.svelte         |   7 +-
 web/src/lib/embed/embed-editor.svelte              |   5 +
 web/src/lib/workflow-editor/credentials.test.ts    |  45 ++-
 web/src/lib/workflow-editor/credentials.ts         |  36 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   5 +
 24 files changed, 956 insertions(+), 101 deletions(-)
```
