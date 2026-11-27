---
id: FEAT-xzdn35
title: 'Map legacy n8n types with native equivalents: Cron, Interval, Function, Item Lists, Read Binary File'
status: doing
priority: medium
labels:
    - n8n
    - importer
parent: EPIC-8rbys7
created: "2026-09-23T01:17:55Z"
updated: "2026-09-23T01:17:55Z"
---

# Description

Legacy types still appear in 5.2% of templates, mostly the most-viewed ones, and every one of them has a native equivalent already.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-18). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Older and still heavily viewed templates use legacy types: `function` (16 templates), `readBinaryFile` (13), `cron` (11), `itemLists` (11), `spreadsheetFile` (9), `writeBinaryFile` (7), `functionItem` (5) and `interval` (1). The cluster appears in 5.2% of templates overall, mostly the most-viewed. n8n Pulse still ranks Cron #28.

# Steps to Reproduce

Import Manual → X using real instances: cron 1053 (`triggerTimes`), interval 1599, function 817, itemLists v3 3427 (`operation: aggregateItems`), readBinaryFile, spreadsheetFile.

# Expected

Cron and Interval map to Schedule. Item Lists maps per operation onto the matching native node. Function and FunctionItem map to foreignCode (or to the NG-1 runner). A placeholder names the closest native node when one exists.

# Actual

- All are generic blocking placeholders with no replacement hint. For example: "KilasFlow has no equivalent of the n8n node \"n8n-nodes-base.cron\"."
- `kilasflow.schedule` could run Cron and Interval. Every Item Lists operation (aggregate, split out, sort, limit, remove duplicates, summarize) is already a native node.
- Function/FunctionItem get the generic placeholder instead of `foreignCode`, so they lose the source-preserving refusal and the replacement suggestion FEAT-8qyfh1 promised for "every JavaScript escape hatch".

# Acceptance Criteria
- [x] Cron and Interval map to Schedule
- [x] Item Lists maps per operation onto aggregate/splitOut/sort/limit/removeDuplicates/summarize
- [x] Function and FunctionItem map to the Code path (foreignCode, or the JS runtime once it exists)
- [x] A placeholder names the closest native node when one exists

# Implementation Plan

Add the mapping entries with translators, and give `placeholderFor` an optional replacement hint table.

# Notes

Related tickets: FEAT-8qyfh1

Related (from the audit): FEAT-8qyfh1 (done; its "one refusal mechanism" does not cover `function`/`functionItem`)

# Related Files

`results-extra.json` (Cron, Interval, Function legacy, Item Lists, Read Binary File legacy, Spreadsheet File legacy)

# Attachments

## Progress

2026-11-27. Most of this ticket had already landed on main in #33 (0ce9afe8, the importer lane) without the ticket being updated. Each criterion was verified against the code and its tests:

- Cron and Interval map to Schedule: `internal/interop/n8n/n8n.go:318` and `:322` (`kilasflow.schedule`, import-only), translators `cronToKilas` (`parameters.go:2918`, triggerTimes) and `intervalToKilas` (`parameters.go:3034`, interval and unit). Tests: `legacy_import_test.go` `TestALegacyCronNodeMapsOntoSchedule`, `TestALegacyIntervalNodeMapsOntoSchedule` and neighbours.
- Item Lists maps per operation: `n8n.go:402` with `kilasTypeFor: itemListsKilasType`, `itemListsToKilas` (`parameters.go:5119`), and `refuseUnknownItemListsOperation` for the unmapped operations. Test: `TestItemListsOperationsMapOntoTheirNativeNodes`.
- Function and FunctionItem map to the Code path: `n8n.go:425` and `:429` land on `kilasflow.foreignCode` (the source-preserving refusal and replacement suggestion), `legacyFunctionToKilas` at `parameters.go:3669`, mode `runOnceForAllItems` / `runOnceForEachItem`. Test: `TestTheLegacyFunctionNodesLandOnTheCodePathPlaceholder`. They deliberately do not run on the JS runtime: their code is written against the legacy sandbox (`items`, `$item`, `getWorkflowStaticData`) that the Code node's globals do not provide.
- Placeholder names the closest native node: `replacementHints` (`n8n.go:1166`) already named Extract From File for readBinaryFile and spreadsheetFile. Added today: `readBinaryFiles` (Extract From File) and the legacy `start` node (Manual Trigger), with test cases in `TestALegacyPlaceholderNamesTheClosestNativeNode`. `writeBinaryFile` is in the new deliberately-unavailable table (`n8n.go:1209`, FEAT-t38djq), because it writes to the host's disk: it points at Google Drive, HTTP Request and Data table.

Remaining: nothing against the acceptance criteria. If the JS runtime ever grows the legacy Function sandbox globals, Function and FunctionItem could run instead of refusing; that is a separate decision, not part of this ticket.
