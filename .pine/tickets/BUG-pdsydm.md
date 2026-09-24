---
id: BUG-pdsydm
title: 'Code node: $json, $binary and $itemIndex are undefined in all-items mode, where n8n reads the first item'
status: testing
priority: medium
labels:
    - code-node
    - javascript
    - n8n
parent: EPIC-tjnr1z
created: "2026-09-24T14:06:57Z"
updated: "2026-09-24T14:06:57Z"
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
