---
id: BUG-14gp8r
title: $('X').item answers by position after a node that reorders items downstream of a fan-out
status: testing
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
- [x] After a fan-out, a node that reorders or filters keeps each item paired with
      the item of the fan-out it came from, for expressions and for the Code
      node's `$('X').item` alike (one algorithm, `engine.PairedIndex`).
- [x] The Code node's explicit `pairedItem` and identity lineage survive a
      fan-out upstream.
- [x] Remove the `test.fail` marker from the js-code.spec.ts lineage case; it
      must pass.
- [x] An engine test covers Split Out → Sort → expression.

# Related Files
- internal/engine/runner.go (`stampProvenance`, `pairIndex`, `originKeyOf`)
- internal/jsrun/items.go (`decoder.paired`)
- e2e/tests/js-code.spec.ts

## Progress 2026-09-23 (checked after main merged into stabilise)

Still reproduces, unchanged, after the merge that brought stabilise's lineage
work (BUG-zf4pnj) together with the Code node (`9af3a81`). With `test.fail`
removed, the js-code.spec.ts lineage case reads `o1, o2, o3` for `o2, o4, o1`:
the same positional answer, not a refusal. So `test.fail` stays.

Why BUG-zf4pnj does not reach it: its exact pointer (`namedItemPosition`, now
the first rule in `pairIndex`) is followed only for an item of X that had no
origin of its own, which is where the runner writes a pointer. Split Out's
items inherit their input's origin, so all four share one, and `pairIndex`
falls through to the `matches > 1` positional fallback that this ticket is
about. The fix is still to walk the lineage node by node, or to record each
item's position in the node that delivered it.

## Plan 2026-09-24

- RED first: engine tests for Split Out → Sort → Set (o2, o4, o1), a Filter
  in the chain, two fan-outs in series with a global reorder read at every
  level, and a Split Out whose split items would otherwise collide with a
  split item's own stamp; a Code node test for `$('X').item` in code and for
  the Code node's `pairedItem` and identity lineage after an upstream fan-out.
- Fix in the runner, not in any executor: when a node's finished output holds
  several items that share one origin (a fan-out), each of those items is
  re-stamped as its own origin — this node, run, port, position — which
  remembers the origin it shared as its parent. Everything downstream
  inherits that stamp, so a reorder or a filter carries it along.
- `pairIndex` walks the current item's stamp and then its parents, and the
  first level that names exactly one item of X is the answer. When no level
  does, the answer is exactly what it was before this ticket.
- Remove `test.fail` from the js-code.spec.ts lineage case and run the spec.

## Progress 2026-09-24 (the fix)

**What n8n does, in my words.** n8n stores on every output item only the
position of the input item it came from (and which input), and each node's
run records which node, output and run fed it. `$('X').item` starts at the
current item and follows those one-step links back through the run data, node
by node, until it reaches X. A reorder cannot confuse it, because the link
travels with the item rather than being implied by position.

**Design chosen: a fan-out's items become origins of their own.** Two designs
were on the table: walk node by node as n8n does, or keep KilasFlow's
flattened origins and record more where they stop being enough. Walking node
by node would replace the root-origin model that every lineage test (BUG-zf4pnj
and friends) is written against, and it needs every intermediate node's run
at hand when pairing, which `request.NodeItems` does not keep (only each
node's latest run), so a loop would break it. The flattened origin is only
ambiguous in one place: after a fan-out, where several items share it. So:

- `anchorFanOuts` (internal/engine/runner.go), called from `runState.complete`
  (the one place every finished invocation passes, whoever stamped it), finds
  items of the node's output — across ports — that share a non-lost origin
  and re-stamps each as this node, run, port and position, with the shared
  origin as its new `PairedItem.Parent`. Unique and lost origins are left as
  they were, so one-to-one chains and every lost/pointer stamp BUG-zf4pnj
  relies on are unchanged.
- The stamp travels with the item: the runner's `cloneItem`, Sort's
  `cloneItems`, Filter's `routedItem`, the Code node's `decoder.inherit` all
  copy it. No executor changes, and any Go-side reorder (including Sort's
  `code` comparator, being added in parallel) keeps lineage without Sort-
  specific code.
- `pairIndex` (one algorithm for expressions and the Code node's
  `engine.PairedIndex`) compares the current item's origin with X's items,
  and while nothing matches, steps to each `Parent` in turn (also trying the
  BUG-zf4pnj exact pointer at each level). If no level names exactly one item,
  the fallbacks run on the last level, which is the root the item carried
  before this ticket, so every answer this change does not improve is the
  answer it was before.
- An anchor's key carries a marker (`originKeyOf`): Split Out's own stamp for
  an item whose input had no origin names Split Out with an input position,
  which can hold the same numbers as an anchor's output position.
  `TestDollarItemTellsASplitItemFromOneThatKeptItsSplitStamp` fails without it.
- Memory: a PairedItem is never changed once written, so copies of an item
  share its parents in memory. Depth grows by one per fan-out only up to
  `maxLineageDepth` (16); `boundedLineage` then drops the middle levels and
  keeps the item's own origin, the nearest fan-outs and the root, so a loop
  that fans out on every pass cannot grow a stamp without bound.

**Why `decoder.paired` did not need to change.** The worry was that the
decoder could only inherit its input item's root origin. After this change an
input item that came out of a fan-out carries its own origin, so inheriting it
is pointing at that input item, and a Code node that fans out itself (several
items with one `pairedItem`) is anchored by the runner like any other node.
`TestTheCodeNodesLineageSurvivesAFanOutUpstream` and
`TestACodeNodeThatFansOutPairsEachItemItMade` cover both.

Tests:
- internal/engine/fanout_lineage_test.go: Split Out → Sort → Set (o2, o4, o1),
  the ticket's case; Split Out → Filter → Sort; two fan-outs in series with a
  global sort, read above, between and after them; the Split Out stamp
  collision.
- internal/engine/lineage_internal_test.go: the depth bound, and unique and
  lost origins left alone.
- nodes/jscode_lineage_test.go: the e2e Rank node in Go; `$('Split Out').item`
  in code after a Sort; a Code node that is the fan-out.
- e2e/tests/js-code.spec.ts: `test.fail` removed from the lineage case.

RED: `o2/o1, o4/o2, o1/o4` for the ticket's case, the Code node case read
`o1, o2, o3` exactly as in the e2e, two fan-outs refused with `node "Orders"
produced several items paired with this one`. GREEN: all of the above, the
full `go test ./...`, `-race` on engine, workflow and nodes, and
`e2e/tests/js-code.spec.ts` 11/11 (the lineage case through worker
processes). `node-coverage` and `n8n-compare` pass except three tests that
expect a 422 at run time and get 202; they fail identically on e7b0603
without this change.

# Attachments
