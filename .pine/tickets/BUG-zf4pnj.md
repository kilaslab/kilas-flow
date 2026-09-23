---
id: BUG-zf4pnj
title: $('Node').item fails after count-changing nodes and error-output splits (BUG-rrkjrd still reproduces)
status: doing
priority: high
labels:
    - engine
    - expressions
    - lineage
    - regression
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T04:26:41Z"
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

# Attachments
