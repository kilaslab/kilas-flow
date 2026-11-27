---
id: FEAT-t38djq
title: Data-utility nodes (Rename Keys, Compare Datasets, Redis) and a 'deliberately unsupported' reason table
status: doing
priority: low
labels:
    - n8n
    - parity
    - node-catalog
parent: EPIC-8rbys7
created: "2026-09-23T01:17:55Z"
updated: "2026-09-23T01:17:55Z"
---

# Description

The server-local nodes (Execute Command, Read/Write Files, Local File Trigger) are probably deliberately absent from a multi-tenant engine, but the diagnostic does not say so.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-22). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** - Read/Write Files appears in 12 templates, Execute Command in 7, Local File Trigger in 3, FTP in 4 and SSH in 3. These server-local nodes appear in 3.9% of templates overall, mostly the most-viewed.
- Redis appears in 9 templates, n8n API node in 7, Compare Datasets in 5, MongoDB in 4 and Execution Data in 3. These data-utility nodes appear in 3.0%.
- n8n Pulse ranks Redis #36 and Execute Command #45.

# Steps to Reproduce

Import Manual → X for each node, using real instances: executeCommand 2334, readWriteFile 2339, localFileTrigger 2335, redis 19669 (`operation`, `key`, `value`, `ttl`), compareDatasets 1964, executionData 2772, plus a docs-derived renameKeys node.

# Expected

Either a tenant-scoped implementation, or a placeholder that states the node is intentionally unavailable and names the alternative. Redis, Rename Keys and Compare Datasets are ordinary gaps.

# Actual

- Every one is a generic blocking placeholder.
- For the server-local nodes, the omission is probably deliberate: shell and disk access are unsafe in a multi-tenant embedded engine. But the diagnostic does not say so or point to Drive, HTTP or the Datastore instead.
- Rename Keys and Compare Datasets are pure transforms that fit alongside Set and Merge.

# Acceptance Criteria
- [x] Rename Keys and Compare Datasets (4 outputs) import and run
- [ ] Redis, behind a `redis` credential with the same host guard as SQL
- [x] Placeholders for deliberately unsupported nodes say why, and point to the alternative (Drive, HTTP, Datastore)

# Implementation Plan

Add a "deliberately unsupported" reason table to `placeholderFor`. Implement Rename Keys (a Set subset), Compare Datasets (a Merge variant with 4 outputs) and Redis (behind a `redis` credential with the same host guard as SQL).

# Notes

Related (from the audit): none

# Related Files

`results-extra.json` (Execute Command, Read-Write Files from Disk, Local File Trigger, Rename Keys, Compare Datasets, Execution Data), `probes/Redis.json`

# Attachments

## Progress

2026-11-27. Scope for the day: Rename Keys, Compare Datasets and the reason table. Redis is NOT done and is the one remaining acceptance criterion.

Landed:

- Rename Keys: native `kilasflow.renameKeys` (`nodes/data_utility.go`), n8n's own `keys.key[{currentKey,newKey}]` shape, dot paths on both sides, renames applied in order, a missing field left alone, null renamed like any other value, binary and provenance carried. Importer mapping and export in `internal/interop/n8n/data_utility.go`; the editor's empty default row is dropped; the regex replacement list (`additionalOptions`) is not carried and is reported as a blocking issue.
- Compare Datasets: native `kilasflow.compareDatasets` with inputs `input1`/`input2` and outputs `onlyInA`, `same`, `different`, `onlyInB` in n8n's slot order, matching by `mergeByFields.values[{field1,field2}]` (names may differ per side), one-to-one in-order pairing, `skipFields` and `disableDotNotation` options, `resolve` of `includeBoth` (keys/same/different report), `preferInput1` or `preferInput2`. Importer mapping and export; options the node does not implement (resolve `mix`, `fuzzyCompare`, `multipleMatches` other than first) refuse to the placeholder with a reason, so a workflow never runs with a different comparison than it was written for. The importer wires n8n output index N to the Nth native port through the catalogue; an import test proves all six wires and the export slots.
- Reason table: `deliberatelyUnavailable` (`n8n.go:1209`) for executeCommand, readWriteFile, writeBinaryFile, localFileTrigger, ssh and ftp. The placeholder says the node is deliberately absent in a shared, multi-tenant engine and names the alternative (Google Drive, HTTP Request, Webhook, Google Drive Trigger, Data table, Code). An ordinary gap keeps the plain "no equivalent" sentence, with a test that the table never excuses one.
- Two builtin glyphs (`key-round`, `git-compare`) added to `glyphs.json` and `node-visual.ts`; node catalogue and catalogue-size facts regenerated; n8n migration guide updated; e2e coverage tiers and a run test per node added.

Not verified here: the vitest glyph test and the Playwright e2e tests (no `web/node_modules` or e2e dependencies in this worktree). The Go-side glyph test passes.

Remaining:

- Redis behind a `redis` credential with the same host guard as SQL (acceptance criterion 2). Not started.
- Rename Keys regular-expression replacements, if wanted (currently a blocking import issue).
- Compare Datasets: fuzzy comparison, all matches, and a mix of versions per field (currently refused on import).
- The Same output carries Input A's item as-is; if real n8n workflows read a `keys`/`same`/`different` shape from the Same branch, that needs a check against a live n8n instance.
