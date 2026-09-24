---
id: BUG-kvpx6x
title: 'Code node: the legacy $items() root is not defined, where n8n still answers it'
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

Found by the Code-node compatibility corpus (FEAT-afkx3k): 5 of the 330 measured JavaScript bodies among the 500 most-viewed n8n templates call the legacy `$items(...)` root, and fail on KilasFlow with `ReferenceError: $items is not defined`.

**What n8n does** (read from n8n's workflow data proxy outside this repository; behaviour only): `$items(nodeName?, outputIndex?, runIndex?)` is an undocumented legacy root that is still installed in the Code node. With a node name it returns that node's output items (`{ json, binary, ... }`) for the given output (default 0) and run (default the last run); with no name it returns the current node's input items. It predates `$('Node').all()`, which returns the same items.

**What KilasFlow does:** `$items` is not defined at all.

# Minimal reproduction

All-items mode, where a node named `Orders` produced `[{ id: 1 }, { id: 2 }]`:

```js
return $items('Orders').map((item) => ({ json: item.json }))
```

- n8n: `[{ id: 1 }, { id: 2 }]`
- KilasFlow: `ReferenceError: $items is not defined [line 1]`

# Acceptance Criteria

- [x] `$items()` returns the input items and `$items('Name')` the named node's items, as `$('Name').all()` does; an output index or run other than the first is refused in the one refusal sentence, as `.all(branch, run)` is.
- [x] The analyser, the expression engine and the Code node agree on `$items`, or the difference is a named error.
- [x] The corpus baseline is regenerated with `make js-corpus-baseline`.

# Notes

## n8n's behaviour, settled from its source (2026-09-24)

Read in the official image `docker.n8n.io/n8nio/n8n:2.33.7` (the workflow data proxy the Code node's task runner spreads into the sandbox), outside this repository. Behaviour only, in my own words.

- `$items` is present in both Code-node modes; it is marked in n8n's source as undocumented legacy syntax.
- With no node name (the name argument `undefined`), it returns the node's own input items: the same list the all-items `items` is (cut to the first item when the node before is set to execute once).
- With a name, the output index defaults to 0 for any falsy value (`undefined`, `null`, `0`), the run index defaults to the node's last run when omitted, and it returns that node's output items for that output and run, the same items `$('Name').all()` reads. A node that does not exist or has not run is an error.

## Plan and decisions

- The Code node defines `$items(name?, output?, run?)` in `runtime.js`: no name (or `null`, to agree with the expression engine, which has always read a null name as "no name") returns `input`, the very list `items` is; a name returns `$(name).all()`, the same array object. An output other than `undefined`/`null`/0, or a run other than `undefined`/0, is refused in the one refusal sentence (`reads $items("X") with an output or run other than the first`), exactly the cases `.all(branch, run)` refuses.
- **The expression engine** used to ignore the second and third arguments, so `$items('X', 1)` silently read output 0. It now fails with a named error for the same cases (`legacyItems` in `internal/expression/roots.go`, shared by the builtin and `callRoot`), so the two agree.
- **The analyser** refuses nothing about `$items`, as it refuses nothing about `.all(1)`: whether the arguments are literals is rarely knowable, so both are run-time refusals in the one sentence. That is where they already agreed.

## Progress (2026-09-24)

- Tests: `TestTheLegacyItemsRootReadsTheInputOrANamedNode` (jsrun), `TestLegacyItemsReadsTheFirstOutputAndRunOnly` (expression), and `$items` added to the sandbox's global list.
- Corpus: all five `$items is not defined` bodies now run (2799/21, 3617/12, 4247/9, 4551/22, 5407/3); accepted-and-ran 237 → 242 (73.8%). Baseline regenerated with `make js-corpus-baseline`.
- Docs: n8n-migration.md (globals list and the run-time refusals), expression-grammar.md and the expressions skill's root reference.
