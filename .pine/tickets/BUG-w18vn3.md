---
id: BUG-w18vn3
title: n8n import rejects a whole file when a node's typeVersion is a string
status: done
priority: medium
labels:
    - import
    - n8n
created: "2026-09-25T09:56:46Z"
updated: "2026-09-25T10:19:47Z"
---

# Description

Found by the 2026-09-25 Code-node self-test. Template 4028 has `"typeVersion": "3.4"` (a string) on one node. n8n loads it; KilasFlow's n8n import rejects the whole file with 422 and a raw Go message ("cannot unmarshal string into Go struct field Document.nodes.5.typeVersion"). With the value turned into a number it imports fine.

# Acceptance Criteria
- [x] A numeric string typeVersion imports as that number (n8n tolerates it).
- [x] A typeVersion that is not a number at all gets a plain diagnostic naming the node, not a Go unmarshal error.
- [x] No raw Go decode errors reach the import response for other loosely typed fields that n8n tolerates in the same way (check the Document struct).

# Notes

## Plan (2026-09-25)

What n8n does, read black-box from the 2.33.7 image's compiled `n8n-core` (`workflow-execute.js`), in my own words: n8n stores workflow JSON without a schema check. Its engine reads `retryOnFail`, `executeOnce`, `alwaysOutputData` and (on the run path) `disabled` with a strict `=== true` test. It feeds `maxTries` and `waitBetweenTries` through `Math.min`/`Math.max`, which coerce a numeric string to its number. It looks a versioned node up by using `typeVersion` as an object key, so `"3.4"` and `3.4` find the same version.

Plan: `Node` and `Target` get their own `UnmarshalJSON` (new file `internal/interop/n8n/loose.go`). The numeric node fields (`typeVersion`, `maxTries`, `waitBetweenTries`, `position`) and a connection's `index` accept a number or a numeric string. The flags are on only when literally `true`. A value that is not a number reads as absent and is recorded on the node, and `Import` reports it. Any other value of the wrong JSON kind is refused in words that name the node and the field.

Decisions:
- A non-numeric `typeVersion` is `lossy`, not `blocking`. The node still lands on a version (the mapping's default, the same as a node that names none), and the workflow can activate. Whether its parameters mean the same there is for the author to judge. `maxTries`, `waitBetweenTries` and `position` are `dropped`, since nothing of the value survived.
- A connection `index` that is not a whole number still refuses the file, now with a plain message naming the target node. Guessing which input the edge lands on would wire the workflow differently.
- A non-boolean flag gets no diagnostic. It is read exactly as n8n's engine reads it, so nothing was lost.
- String fields (`name`, `type`, `id`, …) are not coerced, because n8n cannot use a non-string there either. A mistyped one is refused in plain words, for example `node "Edit Fields" has a "parameters" that is a list, where n8n writes an object`. A top-level array (`n8n export:workflow --all`) is refused with "the file is a list of workflows; import each workflow on its own".

## Progress (2026-09-25)

Done, awaiting review.

- `internal/interop/n8n/loose.go`: `Node.UnmarshalJSON`, `Target.UnmarshalJSON`, `plainDecodeError`, `unreadableIssues`.
- `internal/interop/n8n/n8n.go`: `Node.unreadable` records what could not be read. `Import` reports it for every node, the placeholder included, and rewords a decode error.
- Tests: `internal/interop/n8n/loose_json_test.go` covers a string typeVersion on Set (`"3.4"`) and Code (`"2"`, the repro's shape), a non-numeric typeVersion, numeric strings in maxTries/waitBetweenTries/position/index, a non-numeric maxTries, non-boolean flags, and three plain-word refusals. RED showed the Go decode error for every case. They are GREEN now. The repro `14-string-typeVersion.json` imports and compiles.
- Docs: the node-level elements section of the migration guide. CHANGELOG: Fixed.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `4ee3d1a9` (last commit at or before ticket created 2026-09-25)
- Commits (1):
  - `4d769994` — BUG-w18vn3: n8n import reads a numeric field written as a string the way n8n does, and names any field it still cannot read
- Files changed (the ticket's own commits, d9a7d62..4d76999):

```
 .pine/tickets/BUG-w18vn3.md                   |  34 ++++++-
 CHANGELOG.md                                  |   8 ++
 docs/src/content/docs/guides/n8n-migration.md |  14 +++
 internal/interop/n8n/loose.go                 | 252 ++++++++++++++++++++++++++++++++++++++++++++++
 internal/interop/n8n/loose_json_test.go       | 239 +++++++++++++++++++++++++++++++++++++++++++
 internal/interop/n8n/n8n.go                   |  11 +-
 6 files changed, 552 insertions(+), 6 deletions(-)
```
