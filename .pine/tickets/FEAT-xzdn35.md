---
id: FEAT-xzdn35
title: 'Map legacy n8n types with native equivalents: Cron, Interval, Function, Item Lists, Read Binary File'
status: done
priority: medium
labels:
    - n8n
    - importer
parent: EPIC-8rbys7
created: "2026-09-23T01:17:55Z"
updated: "2026-11-27T08:51:27Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-11-27.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (5):
  - `a7c3ed34` — merge: Rename Keys and Compare Datasets nodes, importer reason table and legacy hints (FEAT-t38djq, FEAT-xzdn35)
  - `d5a202b5` — FEAT-xzdn35, FEAT-t38djq: record progress, tick the criteria that are met
  - `792ffe6a` — FEAT-xzdn35: name the native node for the legacy Start and plural Read Binary Files placeholders
  - `0ce9afe8` — feat(cli): n8n legacy import + workflow verbs — importer lane (#33)
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .env.example                      |    8 +-
 .github/assets/editor.png         |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml          |  108 +++-
 .github/workflows/code-corpus.yml |   54 ++
 .gitignore                        |    4 +
 .pine/MEMORY.md                   |    2 +
 .pine/memory/code-node.md         |   30 +-
 .pine/memory/docs.md              |    5 +-
 .pine/memory/e2e.md               |    3 +
 .pine/memory/licensing.md         |   15 +-
 .pine/memory/n8n-reference.md     |   10 +-
 .pine/memory/persistence.md       |    6 +-
 .pine/memory/testing.md           |    4 +-
 .pine/memory/web-editor.md        |    7 +-
 .pine/roadmap.md                  |    8 +-
 .pine/tickets/BUG-0592hz.md       |   62 ++
 .pine/tickets/BUG-0bzsa1.md       |  464 +++++++++++++++
 .pine/tickets/BUG-0grt9g.md       |  427 +++++++++++++
 .pine/tickets/BUG-0xv7bg.md       |   68 +++
 .pine/tickets/BUG-14gp8r.md       |  198 +++++++
 .pine/tickets/BUG-15st2k.md       |  198 +++++++
 .pine/tickets/BUG-1vh6k6.md       |   51 ++
 .pine/tickets/BUG-2d1pdx.md       |   49 ++
 .pine/tickets/BUG-2eryxn.md       |   95 +++
 .pine/tickets/BUG-2mes2k.md       |   83 +++
 .pine/tickets/BUG-2n4rfz.md       |   58 ++
 .pine/tickets/BUG-2vcwjf.md       |  126 ++++
 .pine/tickets/BUG-2xrz6c.md       |   51 ++
 .pine/tickets/BUG-2z8geh.md       |  136 +++++
 .pine/tickets/BUG-3k12ky.md       |  187 ++++++
 .pine/tickets/BUG-3mem9s.md       |  105 ++++
 .pine/tickets/BUG-3qxx0j.md       |   70 +++
 .pine/tickets/BUG-46g75c.md       |  219 +++++++
 .pine/tickets/BUG-49vf3j.md       |  428 ++++++++++++++
 .pine/tickets/BUG-4ch186.md       |   85 +++
 .pine/tickets/BUG-548bk9.md       |  179 ++++++
 .pine/tickets/BUG-56qqgx.md       |   88 +++
 .pine/tickets/BUG-5bgx5c.md       |   81 +++
 .pine/tickets/BUG-5dn8hr.md       |   39 ++
 .pine/tickets/BUG-5fhcx7.md       |   50 ++
 .pine/tickets/BUG-5xkexq.md       |   63 ++
 .pine/tickets/BUG-605n21.md       |  154 +++++
 .pine/tickets/BUG-66fhea.md       |  112 ++++
 .pine/tickets/BUG-6d6wbg.md       |  115 ++++
 .pine/tickets/BUG-6gkd12.md       | 1183 +++++++++++++++++++++++++++++++++++++
 .pine/tickets/BUG-719gaz.md       |  117 ++++
 .pine/tickets/BUG-745mh2.md       |   60 ++
 .pine/tickets/BUG-8xz9t1.md       |  511 ++++++++++++++++
 .pine/tickets/BUG-9296bf.md       |  532 +++++++++++++++++
 .pine/tickets/BUG-9dw5me.md       |   66 +++
 ...
 1646 files changed, 275849 insertions(+), 7906 deletions(-)
```
