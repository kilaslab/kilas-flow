---
id: BUG-kvpx6x
title: 'Code node: the legacy $items() root is not defined, where n8n still answers it'
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

## Fix round 1 (2026-09-24): one output of the latest run, not "the first"

Review found that the first version accepted an explicit output 0 or run 0 without reading n8n's output 0 or run 0: a node's items are every output one after another (an IF's `$items('IF', 0)` returned both branches), and they are the node's **latest** run (`$items('X', 0, 0)` in a loop read the latest run silently), while `$items('X', 0, $runIndex)`, the lockstep loop pattern, was refused once `$runIndex` > 0. `.all(branch, run)` in the Code node had the same rule.

n8n, from its source (the same image; behaviour only):
- `$items(name, output, run)`: the output is 0 for any falsy value, otherwise the number given; the run is the node's last run when omitted, otherwise the one named (-1 also means the last). An output or run the node does not have is an error.
- `$('X').all(branch, run)`: the branch defaults to the output of X that feeds the current node (0 when none does); the run defaults to the last.

Now, in both engines, in the same words:
- The runner records how many items each output produced (`expression.NodeItem.OutputLengths`, filled by `nodeItemFor`; nil in an older checkpoint, which reads as one output), and the Code node gets them with the run index (`jsrun.NodeView.Outputs`, `.RunIndex`).
- `$items('X')` reads output 0; `$items('X', n)` output n; `.all(n)` in the Code node output n. `.all()` with no branch still reads every output: which output feeds this node is not known to the runtime (n8n's default); noted in the docs.
- A run is read when it is the node's latest, named by number (so `$items('X', 0, $runIndex)` works in lockstep) or as -1. An earlier run is refused: in code, `this node's code reads run 0 of node "X", but only its latest run, 2, is kept, which this server does not run. …`; in an expression, `$items() reads run 0 of node "X", but only its latest run, 2, is kept`. A run the node does not have (later, negative, `null`) is an error, `… names run 3 of node "X", which has no such run`, as n8n errors on it, including `null` (n8n reads a null run as an index and fails), in both engines and the js-diff harness. An output it does not have: `… names output 2 of node "X", which has no such output`.
- An expression's `$('X').all()` still takes no arguments; one with arguments is the arity error it always was, a named error.
- Refusing an earlier run is a thrown Error the code can catch, like every run-time refusal here; corpus 2063 reads `$items(…, 0, counter)` in a try/catch, and in the instrument (one run) counter 1 is "has no run 1" and caught. n8n would answer an earlier run; KilasFlow keeps only the latest (BUG-c19kyx tickets flagging literal run arguments at import and save).

Tests: `TestLegacyItemsReadsOneOutputOfTheLatestRun` (expression: IF and Switch outputs, an old checkpoint, lockstep `$runIndex`, -1, earlier/later/null runs), `TestAllAndItemsReadOneOutputOfTheLatestRun` (jsrun), `TestItemsReadsTheSameOutputAndRunInCodeAndInAnExpression` (nodes: code and expression agree), `TestACodeNodeReadsOneBranchOfAnIF` (a real run Manual → Split Out → IF → Code; RED without `OutputLengths`: `$items() names output 1 of node "IF", which has no such output`). No corpus body moved.

## Fix round 2 (2026-09-24): runs are numbered as `$runIndex` numbers them

`NodeItem.RunIndex` is `len(state.runs) - 1`, which also counts the empty runs a skipped delivery records (`skip()`, `recordSkips`), while `$runIndex` counts real executions (`state.executions`) and n8n numbers only real runs. So a node delivered nothing once and then run had latest run 1 here and 0 in n8n: `$items('X', 0, 0)` was refused as an earlier run, and a lockstep `$runIndex` could be off by the skips.

- `expression.NodeItem.Executions` records the node's real execution count when its items were recorded (`complete`, from `state.executions`), and `NodeItem.LatestRun()` is `Executions - 1`, falling back to `RunIndex` for a checkpoint written before the field (the same unless a delivery was skipped). `RunIndex` keeps its meaning, which pairing by origin relies on. `$items` in expressions and the Code node's `NodeView.RunIndex` (`nodes/jscode_roots.go`) both read `LatestRun()`.
- A checkpoint written before `OutputLengths` existed has them rebuilt on resume from `PortLengths`, in the order the node's definition gives its outputs (`withOutputLengths` in `internal/engine/runner.go`); a layout that does not account for every item stays unknown, which reads as one output.
- Tests: `TestAReadOfANamedRunCountsOnlyRealRuns` (engine: a node skipped then run; RED without recording `Executions`: "tail recorded as run 1, latest real run 1"); `TestAnOlderCheckpointStillReadsOneOutput` (RED without the rebuild: `[t1 t2 f1]` and "names output 1 of node "Gate", which has no such output"); the Code node side in `TestItemsReadsTheSameOutputAndRunInCodeAndInAnExpression` (a Tail with `RunIndex: 1, Executions: 1` read as run 0; RED passing `node.RunIndex`: "reads run 0 of node "Tail", but only its latest run, 1, is kept").
- BUG-c19kyx widened: any run argument that is not a literal -1 or `$runIndex` (corpus 2063 reads it from a counter).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-24.

- Base: `ae1ea141` (last commit at or before ticket created 2026-09-24)
- Commits (5):
  - `c0da7d8f` — merge: BUG-pdsydm, BUG-kvpx6x, BUG-djp647, BUG-9hx5xm Code-node roots and returns n8n still answers
  - `f85bfba8` — BUG-kvpx6x: the Code (JavaScript) guide describes $items and .all(branch, run) reading one output of a node's latest run
  - `b66926de` — BUG-kvpx6x: a read that names a run numbers a node's runs as $runIndex does, skipped deliveries left out, and an older checkpoint still reads one output
  - `b538c7b6` — BUG-kvpx6x: $items and .all(branch, run) read one output of a node's latest run in code and in expressions alike, a lockstep $runIndex included, and an earlier run is refused rather than answered with the latest
  - `26fbe882` — BUG-kvpx6x: the legacy $items() root reads a Code node's input or a named node's items, another output or run is refused in code and in expressions alike, and five corpus bodies now run
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
