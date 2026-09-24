---
id: BUG-pdsydm
title: 'Code node: $json, $binary and $itemIndex are undefined in all-items mode, where n8n reads the first item'
status: done
priority: medium
labels:
    - code-node
    - javascript
    - n8n
parent: EPIC-tjnr1z
created: "2026-09-24T14:06:57Z"
updated: "2026-09-24T15:33:42Z"
---

# Description

Found by the Code-node compatibility corpus (FEAT-afkx3k): 14 of the 330 measured JavaScript bodies among the 500 most-viewed n8n templates read `$json` while running once for all items, and every one of them fails on KilasFlow with `ReferenceError: $json is only available when the code runs once for each item`.

**What n8n does** (read from n8n's task runner outside this repository; behaviour only, nothing copied): a Code node in "Run Once for All Items" mode is given the whole per-item root set built for item index 0. So `$json` is the first input item's JSON, `$binary` is the first item's binary, `$itemIndex` (and the legacy `$position`) is 0, alongside `items` and `$input`. The editor's linter discourages it, but the runtime answers.

**What KilasFlow does:** `internal/jsrun/js/runtime.js` deliberately leaves `$json` and `$itemIndex` undefined in all-items mode and rewords the ReferenceError to point at `$input.all()` / `$input.first()`.

# Minimal reproduction

All-items mode, input items `[{ json: { name: 'a' } }, { json: { name: 'b' } }]`:

```js
return [{ json: { first: $json.name, index: $itemIndex } }]
```

- n8n: `[{ first: 'a', index: 0 }]`
- KilasFlow: `ReferenceError: $json is only available when the code runs once for each item; use $input.all() or $input.first() [line 1]`

# Acceptance Criteria

- [x] In all-items mode `$json`, `$binary` and `$itemIndex` read the first input item (index 0), as in n8n; per-item mode is unchanged.
- [x] The corpus bodies that fail only for this (see `internal/jsrun/corpus/BASELINE.md`, ReferenceError rows) move, and the baseline is regenerated with `make js-corpus-baseline`.

# Notes

## n8n's behaviour, settled from its source (2026-09-24)

Read in the official image `docker.n8n.io/n8nio/n8n:2.33.7` (the task runner, the Code node's task-runner sandbox and the workflow data proxy), outside this repository. Behaviour only, in my own words; nothing was copied.

- In "Run Once for All Items" the Code node starts its task with item index 0, and the runner builds the same per-item root set it builds in per-item mode for that index and spreads it into the sandbox beside `items`. So every per-item root is present in all-items code, fixed at item 0.
- `$json` is the first *input* item's `json` (the node's own input, not another node's output), the very object `items[0].json` is, so changing one changes the other. With no input items it is `undefined` rather than an error.
- `$binary` is a fresh object holding, for each of the first input item's binary properties, that entry's metadata without its `data` field. An item with no binary gives `{}`; no input items gives `undefined`. It is the same root in per-item mode, for the current item.
- `$itemIndex` is 0, and the legacy `$position` is the same number in both modes.
- Per-item mode builds the roots for each item in turn; nothing there changes.

## Plan

1. RED: roots tests for all-items `$json` (same object as `items[0].json`), `$binary` (a metadata copy, `{}` when none), `$itemIndex`/`$position` = 0, empty input → `undefined`; `$binary`/`$position` in per-item mode.
2. GREEN: the all-items wrapper takes `$json`, `$binary`, `$itemIndex`, `$position` as parameters beside `items` and `$input` (per-item gains `$binary` and `$position`), so they stay redeclarable as the other roots are; `wrapperVersion` moves; the all-items "only available when" advice for `$json`/`$itemIndex` goes.
3. Docs (n8n-migration.md mode table and paragraph), corpus baseline regenerated.

## Progress (2026-09-24)

Done on epic/parity-roots.

- `internal/jsrun/wrapper.go`: all-items code's wrapper takes `$json`, `$binary`, `$itemIndex` and `$position` beside `items` and `$input`; per-item code gains `$binary` and `$position`. They stay wrapper parameters, not globals, so `const $json = …` in a body is still a legal redeclaration, and `wrapperVersion` moved to `jsrun-3` so no cached program runs with the old parameter list.
- `internal/jsrun/js/runtime.js`: `run` passes the first item's roots in all-items mode; `binaryOf` builds `$binary` as a metadata copy (no `data`), `{}` for an item without files, `undefined` without an item. The all-items "only available when the code runs once for each item" advice for `$json`/`$itemIndex` is gone; `items` in per-item code keeps its advice.
- **Decision:** `$binary` and `$position` are added to per-item mode too. n8n's root set is the same in both modes, and a per-item `$binary` that was undefined was the same gap; `$json`/`$itemIndex` per item are unchanged.
- Tests: `TestAllItemsCodeReadsTheFirstItemThroughThePerItemRoots`, `TestPerItemCodeReadsItsOwnItemThroughTheRoots` (replace `TestJsonIsOnlyAvailablePerItem`), and the redeclaration and `typeof` tests updated.
- Corpus: 14 bodies moved off `ReferenceError: $json is only available…`; 13 now run (2749/20, 2749/21, 2790/5, 2883/3, 2896/13, 2896/19, 3192/6, 3859/6, 4849/13, 4966/2, 5230/78, 5657/15, 5941/8) and 4400/15 now stops on a TypeError because the stand-in's `pages` field is an object where the body maps a list (the stand-in's, not the runtime's). Accepted-and-ran 224 → 237 (68.3% → 72.3%). Baseline regenerated with `make js-corpus-baseline`.
- Docs: the mode table and paragraph in `docs/src/content/docs/guides/n8n-migration.md`.
- `make js-diff` (Node 24.16.0): `scripts/js-diff/harness.mjs` now gives code the same roots (the per-item roots in all-items mode, `$binary`, `$items`). Same output 219 → 235; "failed only under Node" 18 → 0. What differs is left to other tickets: 2883/3's JSON.parse message wording (Task 13's V8 wording), the en-CA/id-ID date locales, 3314/18's HTML-like comment, and 2314/4, whose inline file jsrun's instrument has nowhere to store.
- Found on the way: n8n's per-item mode also defines `item` (the current input item); 2137/3 and 2137/21 failed on it. Done in BUG-9hx5xm.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-24.

- Base: `ae1ea141` (last commit at or before ticket created 2026-09-24)
- Commits (4):
  - `c0da7d8f` — merge: BUG-pdsydm, BUG-kvpx6x, BUG-djp647, BUG-9hx5xm Code-node roots and returns n8n still answers
  - `432de094` — BUG-pdsydm: the Code (JavaScript) guide gives all-items code the per-item roots of the first item, and per-item code item
  - `1b7f6d4d` — BUG-pdsydm: the js-diff harness gives code the roots the runtime now gives it (the per-item roots in all-items mode, $binary and $items), so js-diff reads 235 bodies the same where it read 219
  - `2313c0d9` — BUG-pdsydm: all-items code reads $json, $binary and $itemIndex at the first input item, as n8n's does, and 13 corpus bodies now run
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
