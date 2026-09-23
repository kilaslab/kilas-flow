---
id: BUG-p3j233
title: Set field of n8n type 'null' outputs the string "{}" while the report says it writes null
status: todo
priority: low
labels:
    - importer
    - set
parent: EPIC-8rbys7
created: "2026-09-23T01:35:50Z"
updated: "2026-09-23T01:35:50Z"
---

# Description

Template 5170 (Learn JSON basics) produces a different value from n8n, and the diagnostic describes behaviour that doesn't happen.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: n8n-templates; finding ids: TPL-13). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Set's `validateEntry` passes unknown types through `validateFieldType`'s default branch unchanged (`packages/workflow/src/type-validation.ts` l.491-493). The stored value `{}` therefore stays an object.

# Steps to Reproduce

1. Import template 5170 "Learn JSON basics".
2. `kilasflow run <id> --wait`.
3. Read the outputs of the "Null" and "Final Exam" nodes.

# Expected

The value's JSON type survives (an object here, or `null` if the value is null), and the diagnostic matches what actually happens.

# Actual

- `json_example_null: "{}"` and `summary_null: "{}"`: strings.
- The import report said: "this field was assigned n8n's null type… it is carried as a string assignment holding null, which writes the same value".

# Acceptance Criteria
- [ ] A null-typed Set row keeps its original JSON value (an object here, or null)
- [ ] The diagnostic text matches the behaviour

# Implementation Plan

Carry the null-typed row as an `object`/raw row holding its original value, or write JSON null. Reword the diagnostic to match.

# Notes

Related (from the audit): none

# Related Files

`runs/5170.json`, `internal/interop/n8n/parameters.go:146-156`, `agents/n8n-templates/n8nsrc/set_utils.ts`, `n8nsrc/typeval.ts`

# Attachments
