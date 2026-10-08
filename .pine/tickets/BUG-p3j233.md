---
id: BUG-p3j233
title: Set field of n8n type 'null' outputs the string "{}" while the report says it writes null
status: done
priority: low
labels:
    - importer
    - set
parent: EPIC-8rbys7
created: "2026-09-23T01:35:50Z"
updated: "2026-10-08T16:20:00Z"
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
- [x] A null-typed Set row keeps its original JSON value (an object here, or null)
- [x] The diagnostic text matches the behaviour

# Implementation Plan

Carry the null-typed row as an `object`/raw row holding its original value, or write JSON null. Reword the diagnostic to match.

# Notes

Related (from the audit): none

# Related Files

`runs/5170.json`, `internal/interop/n8n/parameters.go:146-156`, `agents/n8n-templates/n8nsrc/set_utils.ts`, `n8nsrc/typeval.ts`

# Attachments

## Fix (2026-10-08)

Behaviour confirmed against n8n's published Set helper and `validateFieldType`. A `null` type has no case, so the value passes through unconverted, and a null or undefined value becomes null from 3.2 on.

The fix is in `nullAssignmentType` (`internal/interop/n8n/parameters.go`), which `setToKilas` calls for every null-typed row:

- **Literal value:** carried as the assignment type that keeps it unchanged (`literalAssignmentType`: object, array, number, boolean or string). 5170's "Null" row becomes an `object` row holding `{}`, so it writes the same value and the editor has a fitting control.
- **Expression, or null:** carried with no declared type (`"type": ""`). The Set executor already writes an untyped row as it arrives, and writes undefined as null, which is exactly n8n's behaviour. Node validation already accepts an untyped row. 5170's "Final Exam" `summary_null` is carried this way.
- **Diagnostic:** reworded to describe what is carried. It now says "carried as an assignment of type \"object\", which writes the same value" or "carried with no declared type, which also writes the value as it arrives; the editor shows the row as a string, and saving the node's fields there makes it one". It no longer says "string assignment holding null".
- **Export:** `setToN8N` writes an ordered row with no type back as n8n's `null` (it used to write `string`), so the round trip is exact.

Tests:

- `internal/interop/n8n/set_null_import_test.go` (5170-shaped Null and Final Exam nodes) checks the carried types and the wording. It then runs both imported nodes through the real Set executor; both write the object `{}`, not the text `"{}"`. It also checks the export writes `null`.
- `nodes/executors_test.go` gains the case "a missing untyped value is null".

Verified: `go test ./internal/interop/... ./nodes/` ok, `go vet` clean on both, and `go test ./internal/guardrails/` ok.
