---
id: BUG-5fhcx7
title: Batch-shaped nodes (Aggregate, Summarize, Limit, Remove Duplicates …) split into one-item calls under continue-on-fail
status: todo
priority: medium
labels:
    - engine
    - parity
created: "2026-09-24T13:16:23Z"
updated: "2026-09-24T13:16:23Z"
---

# Description

Under continue-on-fail, the engine's `perItemTolerance` splits a node's batch
into one-item calls unless the node's definition is `WholeBatch`. That is
right for per-item nodes, but a node whose answer depends on the whole batch
computes something different on one item at a time: Aggregate, Summarize,
Limit, Remove Duplicates, Item Lists-style nodes and the like. Sort had the
same bug and was made `WholeBatch` in FEAT-mammrz; the Code node in P4.

# Acceptance Criteria
- [ ] Every batch-shaped native node is `WholeBatch`; a test enumerates them so
      a new batch node cannot forget it.
- [ ] Under continue-on-fail each such node gives the same output as without
      it when nothing fails.
