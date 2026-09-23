---
id: BUG-zf4pnj
title: $('Node').item fails after count-changing nodes and error-output splits (BUG-rrkjrd still reproduces)
status: done
priority: high
labels:
    - engine
    - expressions
    - lineage
    - regression
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T04:48:11Z"
---

# Description

`$('X').item` is the most common cross-node reference in n8n workflows. It still fails downstream of a Code node that changed the item count, and on both branches of a node with an error output.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-9). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** `$('Code').item` in a node after Code → HTTP resolves through the HTTP items' pairedItem to Code item i. It does not matter whether Code's own items trace back to the trigger.

# Steps to Reproduce

1. `[ux-debug] k lineage plain http`: Go Code emits 3 items → HTTP (all 200) → Set `{{ $('Two urls').item.json.u }}`. 2. `[ux-debug] i paired item lineage`: the same with HTTP `continueErrorOutput` → Success/Failure using `$('Two urls').item`. 3. `[ux-debug] j`: `continueRegularOutput`.

# Expected

When the current item's origin names the referenced node itself, `.item` is that node's item at `origin.itemIndex`. The error-output router should keep each item's input pairing.

# Actual

k fails with `node "After regular": … "value": node "Two urls" changed the item correspondence, so there is no single item to pair with`, although every HTTP item is stamped `{"sourceNodeId":"two-urls","itemIndex":i}`. i fails with `… the item being processed lost its lineage upstream, so there is no single item to pair with`, because every item on both outputs of a node with an error output is stamped `lost: true`. j fails the same way as k. `kilasflow debug eval … "{{ $('Two urls').item.json.u }}"` answers `node "Two urls" produced 3 items; use .all(), .first() or .last()`.

# Acceptance Criteria
- [x] When the current item's origin names the referenced node and run, `.item` resolves to that node's item at `origin.itemIndex`
- [x] Error-output and continue-on-fail items keep their input item's pairing instead of being stamped `lost`
- [x] Workflows k, i and j from the audit (Code → HTTP → Set with `$('Two urls').item`) succeed

# Implementation Plan

In `pairNodeItem`, short-circuit when the item's origin is the referenced node and run. Stamp error-output and continue-on-fail items with their input item's origin instead of Lost.

# Notes

Related tickets: BUG-rrkjrd

Related (from the audit): BUG-rrkjrd (done, critical; the same symptom still reproduces for Code-node sources and error branches)

# Related Files

`$SP/agents/ux-debug/cli/run_k.json`, `cli/run_i.json`, `cli/run_j.json`. internal/engine/runner.go:1995-2050 (`pairNodeItem` compares only `ItemOrigins`, which are "" for a node whose own lineage was lost, and never checks `origin.SourceNodeID == name`), :1900-1905 (count-changing nodes marked Lost).

## Progress 2026-09-23 (the fix)

`pairNodeItem` now checks, before comparing origins, whether the current item's origin names the referenced node's own item (`namedItemPosition`). `expression.NodeItem` gains `NodeID`, `RunIndex` and `PortOffsets`, which `nodeItemFor` fills. When the origin is not lost and names X and X's exposed run, `.item` is `Items[PortOffsets[port]+itemIndex]`, bounds-checked. It is read only when X's item at that position has no origin of its own, because a pointer is only ever written for such an item. Split Out stamps its own name with an input position, and the origin comparison still handles that. Old checkpoints decode `NodeID` as "", which never matches.

Stamping (`internal/engine/runner.go`):
- `stampProvenance` is now a `runState` method. The upstream pointer names the source's run rather than the stamping node's. The run and position are exact when the incoming item carries the lost stamp its source wrote on it. Otherwise they are the item's position in the source's latest run.
  - Superseded by Review fix 3 below: there is no "otherwise" any more. A pointer is written only from a lost stamp the source wrote on its own item, and every other item is stamped lost.
- `runPerItem` stamps what each item produced against that item (`stampPerItem`), so success and error items both keep their input's pairing.
- `errorItem` leaves lineage to the runner, and the node-level tolerated branch is now stamped too.

Tests (`internal/engine/runner_test.go`), with `lineageAuditRun` modelled on the httptest stub of `TestDollarItemResolvesThroughHttpAndALoop`:
- `TestDollarItemReachesACodeNodesItemThroughHttp`: workflow k.
- `TestDollarItemResolvesOnBothBranchesOfAnErrorOutput`: workflow i, both branches.
- `TestDollarItemResolvesAfterAContinueOnFailFailure`: workflow j.
- `TestDollarItemResolvesAfterANodeLevelFailure`: Execute Once plus an error output, which takes the node-level tolerated branch.
- `TestDollarItemPairsWithTheRunThatDeliveredTheItem`: the node after a loop's done port. The pointer names the loop's run.
- `TestDollarItemRefusesAnEarlierRunRatherThanReadingTheLatest`: a branch held behind a loop is refused rather than paired with the last batch.
- `TestDollarItemDoesNotReadAnInputPositionAsAnOutputPosition`: guards the Split Out case.

RED: k and j failed with `node "Two urls" changed the item correspondence`. i and the node-level case failed with `the item being processed lost its lineage upstream`. The loop run case read nothing. Each of the last three fails when the piece it guards is removed. GREEN: `go test ./... -count=1` passes.

`kilasflow debug eval … "{{ $('Two urls').item.json.u }}"` is unchanged. Debug eval has no current item to pair with, so it still answers `node "Two urls" produced 3 items; use .all(), .first() or .last() to choose one`.

Review fix (2026-09-23): the pointer fallback took the source's latest run whenever the incoming item did not carry the source's own lost stamp, so behind IF, Set, Filter or a loop a branch held behind a loop read another batch's item silently. `pointerInto` now writes that pointer only when the source's latest run holds the item at that port and position with the very stamp the incoming item carries. Otherwise the item is stamped lost. `NodeItem.PortLengths` bounds a position to its own port. Tests: `TestDollarItemBehindARoutingNodeNeverReadsAnotherBatch` (RED: `b1-0 read b2-0; b1-1 read b2-1; b0-0 read b2-0; b0-1 read b2-1`; now 2 paired, 4 refused, 0 wrong) and `TestDollarItemStaysWithinThePortItsOriginNames` (RED: a pointer past `true` read `false-0`).

Review fix 2 (2026-09-23): a single run can hold one stamp twice. Set A and Set B copy one item's lineage into a Merge, so a per-item approval resumed on B-x0 at position 0 matched Merge[0] (A-x0). `pointerInto` now also refuses when any other item on the port the item arrived on carries the same stamp. Other ports are not searched, because the edge fixes the port. Tests: `TestDollarItemRefusesAStampTwoItemsOfOnePortShare` (RED: `approved B-x0, and $('Merge').item read A-x0`; now all 4 refused, 0 wrong) and `TestDollarItemPairsWithinThePortTheItemCameFrom` (the same stamp on IF's two ports: B-x0 pairs and B-x1 is refused; searching every port would refuse B-x0). `TestDollarItemBehindARoutingNodeNeverReadsAnotherBatch` now also asserts `b2-0,b2-1` paired and 4 refused.

Review fix 3 (2026-09-23): each round's patch to the positional fallback left another wrong answer. The latest was the same stamp at the same position in two runs of an IF behind a loop: `A-x0 read B-x0; A-x1 read B-x1`. The fallback also scanned the port once per item, which was O(n²). As the controller ruled, it is removed. `pointerInto` now writes a pointer only from a lost stamp the delivering node wrote on its own item; everything else is stamped lost, as before this ticket. k, i, j and the node-level case never used the fallback: they go through the exact pointer and still pass. Behind a lineage pass-through (IF, Set, Merge, a loop), `$('X').item` is now refused rather than read by position. Exact pairing there is the controller's follow-up ticket (record the delivering run and offset on the pending invocation). The acceptance criteria still hold as worded: an error or continue-on-fail item keeps its input's pairing, and behind a pass-through that pairing is already lost. Tests switched to asserting refusal: `TestDollarItemAfterALoopIsRefusedRatherThanGuessed` (formerly `…PairsWithTheRunThatDeliveredTheItem`), `TestDollarItemBehindARoutingNodeNeverReadsAnotherBatch` and `TestDollarItemRefusesAStampSharedAcrossTwoPorts`. New regression tests, each asserting no wrong reads: `TestDollarItemRefusesAStampTwoRunsShare` (RED: `A-x0 read B-x0; A-x1 read B-x1`), `TestDollarItemOffTheLoopPortNeverReadsAnotherBatch`, `TestDollarItemNeverMisreadsAcrossManyItems` and `TestDollarItemIgnoresNodeItemsFromOlderCheckpoints`. The reviewer's 30000-item probe averages 796 ms here, against 793 ms on base and 2.31 s on 287d314.

Review fix 4 (2026-09-23): a sub-workflow's result items came back with the child run's lineage stamps, and the exact pointer trusts a lost stamp that names the delivering node. Node IDs repeat across workflows: Duplicate keeps them, the n8n importer falls back to `n8n-<index>`, and API and CLI documents use readable ones. So a parent's `$('X').item` could follow a child's stamp. When the call node and a child 1→3 node were both `call`, the reads were `x1 read x2; x2 read x0; x0 read x1`. When a count-changing `fan` was on both sides, 6 of 9 items read another call's item. Base refused both. `terminalItems` (`internal/engine/service.go`) now clears `Paired` on the items it hands back. The caller's runner then stamps them as the call's output, so the first shape resolves exactly and the second refuses. This also removes the child's `pairedItem` from a Workflow Tool's result to its agent. Nothing reads a sub-workflow result's lineage: the Execute Workflow node returns the items for the runner to stamp, and the error workflow ignores them. Tests (`internal/engine/subworkflow_test.go`, on a `newCompositionWith` harness that takes test steps):
- `TestDollarItemAfterASubWorkflowReadsTheCallNotAChildNodeOfTheSameID`. RED: `x1 read x2; x2 read x0; x0 read x1`. Now each of x1, x2 and x0 pairs with itself.
- `TestDollarItemRefusesAChildsLineageUnderAParentNodesID`. RED: 6 of 9 read another call's `p`. Now all 9 are refused.

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23. Rewritten on 2026-09-23 to list only this ticket's own commits (`git log --grep "BUG-zf4pnj" 2f8e9b1..5830b4e`) and the files exactly those commits changed.

- Range: `2f8e9b1..5830b4e`
- Commits (6):
  - `179992e1` — BUG-zf4pnj: a sub-workflow's items reach the caller without the child's lineage stamps
  - `9d85313e` — BUG-zf4pnj: an item is paired only through a stamp its source wrote, never by position
  - `287d3143` — BUG-zf4pnj: a stamp two items of a run share pairs with neither of them
  - `9d800372` — BUG-zf4pnj: an item handed on by IF, Set, Filter or a loop never pairs with another run's item
  - `494ff20e` — chore(pine): close BUG-zf4pnj with its landing evidence
  - `203bbc49` — BUG-zf4pnj: $('X').item resolves through a node that changed the item count, and on both branches of an error output
- Merged by (1):
  - `bda50613` — merge: a Wait inside a loop resumes on every batch, and $('Node').item pairs only through a stamp its source wrote (BUG-bw2zc1, BUG-zf4pnj)
- Files changed by those commits (merge commits excluded):

```
 .pine/tickets/BUG-zf4pnj.md         |  319 ++++++++++-
 internal/engine/eval.go             |    2 +-
 internal/engine/runner.go           |  413 +++++++++-----
 internal/engine/runner_test.go      | 1112 ++++++++++++++++++++++++++++++++---
 internal/engine/service.go          |   14 +-
 internal/engine/subworkflow_test.go |  168 +++++-
 internal/expression/roots.go        |   23 +-
 7 files changed, 1818 insertions(+), 233 deletions(-)
```
