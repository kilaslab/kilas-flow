---
id: BUG-kvpx6x
title: 'Code node: the legacy $items() root is not defined, where n8n still answers it'
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

- [ ] `$items()` returns the input items and `$items('Name')` the named node's items, as `$('Name').all()` does; an output index or run other than the first is refused in the one refusal sentence, as `.all(branch, run)` is.
- [ ] The analyser, the expression engine and the Code node agree on `$items`, or the difference is a named error.
- [ ] The corpus baseline is regenerated with `make js-corpus-baseline`.
