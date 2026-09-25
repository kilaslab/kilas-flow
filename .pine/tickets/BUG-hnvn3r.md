---
id: BUG-hnvn3r
title: Always Output Data looks at every output instead of only the first, as n8n does
status: todo
priority: medium
labels:
    - engine
    - n8n
    - parity
created: "2026-09-25T10:10:33Z"
updated: "2026-09-25T10:10:33Z"
---

# Description

Found while settling BUG-4ch186. In n8n, Always Output Data looks only at the node's first output: if that output has no first item, n8n puts one empty item on it (paired with every input item) and leaves the other outputs as the node returned them. KilasFlow's `withEmptyItem` (internal/engine/runner.go) instead leaves the output alone whenever any port carries items, and pairs the empty item with nothing.

The two differ only for a node with several outputs whose first output is empty while another is not: an IF that sent every item to `false`, a Switch that routed nothing to output 0, or a node with `onError: continueErrorOutput` whose items all failed. n8n then also runs the branch on output 0 with one empty item; KilasFlow does not.

# Steps to Reproduce

1. Manual Trigger → IF (condition false for the item, `alwaysOutputData: true`) → a node on `true` and a node on `false`.
2. Run.

# Expected

As n8n: the `false` branch gets the item and the `true` branch gets one empty item.

# Actual

Only the `false` branch runs.

# Acceptance Criteria
- [ ] The empty item is added when the first output has no items, whatever the other outputs carry, and goes on the first output only.
- [ ] The empty item pairs with the node's input items, as n8n's does.
- [ ] Confirm the rule against n8n (source as a behaviour reference, or black-box) before changing it, and record it in your own words.
