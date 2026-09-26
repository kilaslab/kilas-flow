---
id: BUG-2mes2k
title: HTTP Request Tool hands the model only the first element of a JSON-array response
status: done
priority: high
labels:
    - ai
    - ai-tools
    - http
parent: EPIC-8rbys7
created: "2026-09-23T01:56:12Z"
updated: "2026-09-26T16:45:28Z"
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
- [x] The tool result carries the whole response: the raw body, or a JSON array of every item
- [x] The optional response optimisation (truncate or select fields) is explicit, never implicit
- [x] A test with a 3-element array response asserts that the model sees all 3

# Implementation Plan

Marshal every item of `output[0]` (an array when there is more than one), as `workflowTool.Invoke` already does (`nodes/ai.go:2639-2649`). Add a size cap with an explicit truncation note.

# Fix

Confirmed as the ticket says: `decodeItems` (`nodes/http.go`) turns a top-level array into one item per element (a non-object element is carried under `data`), and `httpRequestTool.Invoke` kept `output[0][0]`.

`httpToolObservation` (`nodes/ai.go`) now builds what the model reads. One item is its own object, unchanged; several items are a JSON array of every item's `json` object, in the order the endpoint sent them. It is not `workflowTool.Invoke`'s exact shape: that one marshals whole `workflow.Item` structs, and the HTTP tool's model should read the response, not the engine's `json`/`pairedItem` wrapper.

The optimisation is explicit. `httpToolMaxBytes` is 256 KiB, the same budget `datastoreToolMaxBytes` gives a data table tool. Past it the observation is cut on a character boundary and ends with a `[truncated: ...]` note giving the response's item count and size, how many bytes are shown and how many are omitted, and telling the model the result is partial. Nothing else is dropped or selected. The outbound 8 MiB read limit (`outbound.max_response_bytes`) still applies before this and still sets `truncated: true` on the item.

Tests in `nodes/ai_tools_test.go` read the tool message in the provider's second request, so they pin what the model is handed: a 3-element array (all 3, in order), a single object (byte-for-byte unchanged), and a 360 KiB response of 12 items (cut at the cap with the note, no split character). The array and oversize tests fail on the old code.

Not done, and not part of this ticket: n8n's "Optimize Response" setting (select fields, or a data path) has no equivalent in this tool; FEAT-j5s2n4 lists its import as an unreported drop. A one-element array response still reads as a single object, because the request node's items do not record that the body was an array.

# Notes

Related (from the audit): none

# Related Files

`case-3-1.execution.json`, `case-3-2.execution.json`, `stub-hits.jsonl`. Code `nodes/ai.go:2037` (`json.Marshal(output[0][0].JSON)`): the HTTP executor splits a top-level array into items and the tool keeps item 0.

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-26. `--evidence` diffs from the commit at the ticket's creation, a bulk audit commit, so its own list is replaced here by this fix's commit alone.

- Commits (1):
  - `271b136` — BUG-2mes2k: an agent's HTTP Request Tool hands the model the whole response, so a JSON array reads as every element and not just the first
- Files changed (`git show --stat 271b136`):

```
 .pine/tickets/BUG-2mes2k.md |  22 +++++--
 nodes/ai.go                 |  46 ++++++++++++++-
 nodes/ai_tools_test.go      | 137 ++++++++++++++++++++++++++++++++++++++++++++
 3 files changed, 197 insertions(+), 8 deletions(-)
```

Checks: `go test ./nodes/... ./internal/ai/...` and the whole `go test ./...` pass, and `go vet` and `go build ./...` are clean.
