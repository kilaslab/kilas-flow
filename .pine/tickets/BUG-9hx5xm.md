---
id: BUG-9hx5xm
title: 'Code node: per-item code has no `item` root, where n8n gives it the current item'
status: testing
priority: medium
labels:
    - code-node
    - javascript
    - n8n
parent: EPIC-tjnr1z
created: "2026-09-24T15:12:28Z"
updated: "2026-09-24T15:12:28Z"
---

# Description

Found by the Code-node compatibility corpus (FEAT-afkx3k), noted while fixing BUG-pdsydm: 2 of the measured JavaScript bodies among the 500 most-viewed n8n templates (2137/3 and 2137/21) run once for each item and read `item`, and fail on KilasFlow with `ReferenceError: item is not defined`.

**What n8n does** (read from n8n's task runner in the official image `docker.n8n.io/n8nio/n8n:2.33.7`, outside this repository; behaviour only): in "Run Once for Each Item" mode, before each item's call the runner sets `item` to the current input item itself (the whole `{ json, binary, … }` item, the object `$input.item` is), beside the per-item roots. All-items mode has no `item`.

# Acceptance Criteria

- [x] Per-item code reads `item` as the current input item, the same object as `$input.item`; all-items code has none; a body may still declare its own `item`.
- [x] The corpus bodies that failed only for this move, and the baseline is regenerated.

# Notes

## Progress (2026-09-24)

Done on epic/parity-roots in Task 12's fix round: `item` is the last parameter of the per-item wrapper (`internal/jsrun/wrapper.go`, `wrapperVersion` jsrun-4), passed by `runtime.js`'s `run`; the js-diff harness does the same. Test: `TestPerItemCodeHasTheCurrentItemAsItem`. Corpus: 2137/3 and 2137/21 now run. Docs: the mode table in n8n-migration.md.
