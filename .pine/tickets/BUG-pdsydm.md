---
id: BUG-pdsydm
title: 'Code node: $json, $binary and $itemIndex are undefined in all-items mode, where n8n reads the first item'
status: todo
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

- [ ] In all-items mode `$json`, `$binary` and `$itemIndex` read the first input item (index 0), as in n8n; per-item mode is unchanged.
- [ ] The corpus bodies that fail only for this (see `internal/jsrun/corpus/BASELINE.md`, ReferenceError rows) move, and the baseline is regenerated with `make js-corpus-baseline`.
