---
id: BUG-3qxx0j
title: 'Extract From File: fromJson rejected, absent operation becomes pdf, xlsx/csv import silently then fail'
status: todo
priority: high
labels:
    - n8n
    - importer
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
---

# Description

Extract From File appears in 70 of 998 templates, and 34 of the 94 sampled instances use an operation other than pdf/text. The import reports 0 issues, and the run then fails or reads the file as PDF.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-7). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Extract From File appears in 70/998 templates. Its operations are `csv` (the default when absent), `html`, `fromIcs`, `fromJson`, `ods`, `pdf`, `rtf`, `text`, `xls`, `xlsx`, `xml` and `binaryToPropery`. In the cache, 34 of 94 instances are something other than pdf/text: 11 binaryToPropery, 8 xlsx, 6 fromJson, 4 default csv, 2 xls, 2 xml, 1 ods.

# Steps to Reproduce

1. Import one real instance of each operation into a single workflow: "[node-gap] probe Extract From File ops", wf_01a0cbd1-12b3-72c3-a2ee-90240d24eb46.
2. Run it.
3. Import real template 3647, whose "Extract from JSON" node has `operation: fromJson`.

# Expected

Map `fromJson`→`json`. Default an absent operation to `csv`, not `pdf`. Report a blocking issue at import for any operation the node cannot run. Implement csv/xlsx/binaryToPropery, which cover most of the remainder.

# Actual

- The import reports **0 issues**.
- The run then fails for xlsx, xml, binaryToPropery, xls, ods and fromJson with `operation must be pdf, text or json`.
- n8n's JSON operation is spelled `fromJson`, but the mapper only knows `json`/`extractfromjson`, so a supported operation is rejected.
- A node with no `operation` (n8n's CSV default) imports with the operation absent and silently takes KilasFlow's default, `pdf`.

# Acceptance Criteria
- [ ] `fromJson` maps to `json`
- [ ] An absent operation is treated as n8n's default, `csv`
- [ ] Any operation the node cannot run is a blocking import issue, never a silent run-time failure
- [ ] csv, xlsx and binaryToPropery are implemented (they cover most of the remainder)

# Implementation Plan

Add `fromjson` to the switch. Emit a blocking `Unsupported` for any other operation. Set `csv` when the operation is absent, and block until CSV is implemented.

# Notes

Related tickets: EPIC-hkypt5, FEAT-wsd0vg

Related (from the audit): EPIC-hkypt5 (3647 is one of its target templates). FEAT-wsd0vg (done) covered only PDF/TXT/JSON.

# Related Files

`probes/extract-ops.json`, `internal/interop/n8n/parameters.go:3701-3714` (switch without `fromjson` and without a default), the catalog's `kilasflow.extractFromFile` `operation` default `pdf`.

# Attachments
