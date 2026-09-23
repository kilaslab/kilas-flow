---
id: BUG-jx2g0k
title: runPerItem drops the output of items processed before one that suspends
status: todo
priority: high
labels:
    - engine
    - data-loss
parent: EPIC-8rbys7
created: "2026-09-23T07:35:20Z"
updated: "2026-09-23T07:35:37Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

`runPerItem` runs a per-item node once per input item, assembling each item's output as it goes. When item k suspends (an approval Wait with `continueRegularOutput`, or a Wait with a per-item duration), the items after k are correctly rescheduled as a pending invocation for resume — but the outputs already assembled for items 0..k-1, including tolerated error items, are not carried anywhere. They are neither in the checkpoint's completed output nor in the pending invocation, so resume never delivers them.

# Steps to Reproduce

1. Build a per-item node fed 2+ items, where item 0 fails and is tolerated (`onError` continue) and item 1 suspends (an approval Wait, or a Wait with a per-item duration).
2. Run the workflow: item 0 completes as an error item, item 1 suspends and checkpoints.
3. Resume the execution.
4. Inspect what the downstream node receives.

# Expected

Downstream sees every item's outcome: item 0's tolerated error item and item 1's resumed output.

# Actual

Downstream sees only the resumed item. Item 0's error item is lost — it was assembled before the suspend but never checkpointed or replayed.

# Acceptance Criteria
- [ ] Every item's outcome (including tolerated error items) processed before a suspending item reaches downstream after resume
- [ ] A test covers a per-item node where an earlier item is tolerated and a later item suspends, asserting both outcomes arrive after resume

# Notes

The items after the suspending one are already handled correctly (rescheduled as a pending invocation before the checkpoint is taken, per the comment at runner.go ~1193-1196). The gap is specifically the items processed before the one that suspended.

# Related Files

internal/engine/runner.go runPerItem, ~1188-1199 (the suspend branch inside the per-item loop, which reschedules `items[position+1:]` but does not carry forward `assembled`)
