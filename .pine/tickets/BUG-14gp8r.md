---
id: BUG-14gp8r
title: $('X').item answers by position after a node that reorders items downstream of a fan-out
status: todo
priority: high
parent: EPIC-tjnr1z
created: "2026-09-23T06:41:41Z"
updated: "2026-09-23T06:41:41Z"
---

# Description

The engine pairs `$('X').item` by comparing *root* origins: every item carries
the origin its one-to-one chain started from (`stampProvenance`), and
`pairIndex` (internal/engine/runner.go) looks for the one item of X with the
same origin, falling back to the current item's position when several of X's
items share it (a fan-out, as after Split Out or a Code node that turned one
item into several).

That fallback is only right while the chain keeps the fan-out's order. A node
that reorders or filters items after the fan-out (Sort, Filter, a Code node
that sorts, n8n's Remove Duplicates …) produces items that still share the
fan-out's origin, so `$('X').item` answers with X's item at the current
position: a confident wrong item, the failure the lineage model exists to
prevent. n8n walks `pairedItem` back node by node, so it answers with the item
each one actually came from.

It is not specific to the JavaScript Code node: the native Sort shows it too.
The Code node's own lineage (explicit `pairedItem`, object identity) is
correct, and it is lost because the decoder can only inherit the input item's
root origin, never point at the input item itself.

# Steps to Reproduce

Native nodes, over the API:

1. Manual → Set `orders = [{id:'o1',amount:40},{id:'o2',amount:250},{id:'o4',amount:120}]`
   → Split Out `orders` → Sort `amount:desc` → Set `origin = {{ $('Split Out').item.json.id }}`.
2. Run it.

With the Code node: `e2e/tests/js-code.spec.ts`, "lineage survives a Code node
that filters, reorders and rebuilds items" (marked `test.fail` until this is
fixed).

# Expected

`origin` is the item's own id: o2, o4, o1.

# Actual

`origin` is Split Out's item at the same position: o1, o2, o4.

# Acceptance Criteria
- [ ] After a fan-out, a node that reorders or filters keeps each item paired with
      the item of the fan-out it came from, for expressions and for the Code
      node's `$('X').item` alike (one algorithm, `engine.PairedIndex`).
- [ ] The Code node's explicit `pairedItem` and identity lineage survive a
      fan-out upstream.
- [ ] Remove the `test.fail` marker from the js-code.spec.ts lineage case; it
      must pass.
- [ ] An engine test covers Split Out → Sort → expression.

# Related Files
- internal/engine/runner.go (`stampProvenance`, `pairIndex`, `originKeyOf`)
- internal/jsrun/items.go (`decoder.paired`)
- e2e/tests/js-code.spec.ts

# Attachments
