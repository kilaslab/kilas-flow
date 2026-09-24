---
id: BUG-9hx5xm
title: 'Code node: per-item code has no `item` root, where n8n gives it the current item'
status: done
priority: medium
labels:
    - code-node
    - javascript
    - n8n
parent: EPIC-tjnr1z
created: "2026-09-24T15:12:28Z"
updated: "2026-09-24T15:33:43Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-24.

- Base: `7d5334fd` (last commit at or before ticket created 2026-09-24)
- Commits (2):
  - `c0da7d8f` — merge: BUG-pdsydm, BUG-kvpx6x, BUG-djp647, BUG-9hx5xm Code-node roots and returns n8n still answers
  - `7442889a` — BUG-9hx5xm: per-item code has item, the current input item, as n8n's does, and corpus bodies 2137/3 and 2137/21 now run
- Files changed (the ticket's own commits, d1074f44c3737e566100933cd4d7c00fefdff5aa..c0da7d8fb2170a609742e02e2b1d80d19185511a):

```
 .pine/tickets/BUG-9hx5xm.md                                 |  30 +++++++
 .pine/tickets/BUG-c19kyx.md                                 |  22 +++++
 .pine/tickets/BUG-djp647.md                                 |  39 +++++++-
 .pine/tickets/BUG-kvpx6x.md                                 |  56 +++++++++++-
 .pine/tickets/BUG-pdsydm.md                                 |  37 +++++++-
 .pine/tickets/BUG-qe71kf.md                                 |  22 +++++
 docs/src/content/docs/concepts/safety-boundaries.md         |   6 +-
 docs/src/content/docs/guides/code-javascript.md             |  38 +++++---
 docs/src/content/docs/reference/expression-grammar.md       |   2 +-
 internal/engine/runindex_skip_test.go                       | 105 ++++++++++++++++++++++
 internal/engine/runner.go                                   |  30 ++++++-
 internal/expression/globals.go                              |   5 +-
 internal/expression/parity_test.go                          |  48 ++++++++++
 internal/expression/roots.go                                | 116 ++++++++++++++++++++++--
 internal/jsrun/comparator_test.go                           |   4 +-
 internal/jsrun/corpus/BASELINE.md                           |  57 ++++++------
 internal/jsrun/corpus/baseline.json                         | 120 +++++++++++--------------
 internal/jsrun/corpus/scoreboard_test.go                    |   6 ++
 internal/jsrun/doc.go                                       |   3 +
 internal/jsrun/engine.go                                    |  10 ++-
 internal/jsrun/helpers.go                                   |  31 +++++--
 internal/jsrun/helpers_test.go                              |  99 ++++++++++++++++++++
 internal/jsrun/inline.go                                    | 149 ++++++++++++++++++++++++++++++
 internal/jsrun/items.go                                     |  94 +++++++++++++------
 internal/jsrun/js/runtime.js                                |  94 +++++++++++++++++--
 internal/jsrun/roots.go                                     |   7 ++
 internal/jsrun/roots_test.go                                | 154 +++++++++++++++++++++++++++-----
 internal/jsrun/run.go                                       |  36 ++++++--
 internal/jsrun/testdata/surface.txt                         |   1 +
 internal/jsrun/wire.go                                      |   6 ++
 internal/jsrun/wrapper.go                                   |  15 ++--
 internal/jsworker/doc.go                                    |   7 +-
 internal/jsworker/helpers_test.go                           |  18 ++++
 internal/jsworker/jsworker_test.go                          |  44 +++++++--
 internal/jsworker/pool.go                                   |   4 +-
 nodes/jscode_helpers_test.go                                |  22 +++++
 nodes/jscode_lineage_test.go                                |  45 ++++++++++
 nodes/jscode_roots.go                                       |   2 +-
 nodes/jscode_roots_test.go                                  |  54 +++++++++++
 scripts/js-diff/harness.mjs                                 |  38 +++++---
 skills/kilasflow-expressions/references/EXPRESSION_ROOTS.md |   2 +-
 41 files changed, 1443 insertions(+), 235 deletions(-)
```
