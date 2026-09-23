---
id: BUG-2mes2k
title: HTTP Request Tool hands the model only the first element of a JSON-array response
status: todo
priority: high
labels:
    - ai
    - ai-tools
    - http
parent: EPIC-8rbys7
created: "2026-09-23T01:56:12Z"
updated: "2026-09-23T01:56:12Z"
---

# Description

An agent asked "how many customers?" answered 1 instead of 3. The run was green, so the wrong answer was invisible.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama; finding ids: AI-1). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The HTTP Request Tool returns the whole response body to the model. An array stays an array, and the optional "Optimize Response" setting only truncates or selects fields.

# Steps to Reproduce

1. Start a stub where `GET /users` returns 3 objects (`stub.py`, port 18912).
2. Agent + chatModel (Ollama) + httpTool `list_users` (GET `http://127.0.0.1:18912/users`, description "List every customer…").
3. Ask "How many customers are there in total, and which of them are on the free plan? Use list_users."

# Expected

The tool result carries all 3 rows, either as the raw body or as a JSON array of every item.

# Actual

The observation is `{"city":"Bandung","id":1,"name":"Rina Wijaya","plan":"pro"}`, one object. The agent answers "There is 1 customer in total, and none of them are on the free plan." The truth is 3 customers, and Budi is on free. It reproduced on a second run: "The customers and their plans are: - Rina Wijaya: pro". The execution status is succeeded.

# Acceptance Criteria
- [ ] The tool result carries the whole response: the raw body, or a JSON array of every item
- [ ] The optional response optimisation (truncate or select fields) is explicit, never implicit
- [ ] A test with a 3-element array response asserts that the model sees all 3

# Implementation Plan

Marshal every item of `output[0]` (an array when there is more than one), as `workflowTool.Invoke` already does (`nodes/ai.go:2639-2649`). Add a size cap with an explicit truncation note.

# Notes

Related (from the audit): none

# Related Files

`case-3-1.execution.json`, `case-3-2.execution.json`, `stub-hits.jsonl`. Code `nodes/ai.go:2037` (`json.Marshal(output[0][0].JSON)`): the HTTP executor splits a top-level array into items and the tool keeps item 0.

# Attachments
