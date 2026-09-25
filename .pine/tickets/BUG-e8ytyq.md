---
id: BUG-e8ytyq
title: Merge in chooseBranch mode runs on one input where n8n requires both
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

Found while settling BUG-4ch186. n8n's Merge node (v3) declares which of its inputs must have received items before it may run: in `chooseBranch` mode both inputs are required, in every other mode one is enough. KilasFlow's scheduler runs a node with several item inputs as soon as any one of them delivered (`delivered` in internal/engine/runner.go), so a Merge in `chooseBranch` mode runs on one side only, where n8n does not run it at all.

With `alwaysOutputData` on that Merge the difference grows: n8n emits nothing, KilasFlow hands on an item.

# Steps to Reproduce

1. Manual Trigger → IF; `true` → Merge input 1, `false` → Merge input 2; Merge mode `chooseBranch`.
2. Run with an item that goes only to `true`.

# Expected

As n8n: the Merge does not run (both inputs are required in this mode).

# Actual

The Merge runs with only input 1.

# Acceptance Criteria
- [ ] A node definition can say which item inputs must have delivered before it runs (fixed, or depending on a parameter as Merge's mode does), and the scheduler honours it.
- [ ] Merge in `chooseBranch` mode needs both inputs; other modes need one.
- [ ] Confirm against n8n before changing it, and record the rule in your own words.
