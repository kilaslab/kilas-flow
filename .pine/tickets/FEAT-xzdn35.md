---
id: FEAT-xzdn35
title: 'Map legacy n8n types with native equivalents: Cron, Interval, Function, Item Lists, Read Binary File'
status: todo
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
- [ ] Cron and Interval map to Schedule
- [ ] Item Lists maps per operation onto aggregate/splitOut/sort/limit/removeDuplicates/summarize
- [ ] Function and FunctionItem map to the Code path (foreignCode, or the JS runtime once it exists)
- [ ] A placeholder names the closest native node when one exists

# Implementation Plan

Add the mapping entries with translators, and give `placeholderFor` an optional replacement hint table.

# Notes

Related tickets: FEAT-8qyfh1

Related (from the audit): FEAT-8qyfh1 (done; its "one refusal mechanism" does not cover `function`/`functionItem`)

# Related Files

`results-extra.json` (Cron, Interval, Function legacy, Item Lists, Read Binary File legacy, Spreadsheet File legacy)

# Attachments
