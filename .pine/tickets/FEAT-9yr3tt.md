---
id: FEAT-9yr3tt
title: exact $('X').item pairing through pass-through nodes (IF, Set, Filter, Merge, loops)
status: todo
priority: medium
labels:
    - engine
    - expressions
    - lineage
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-09-23T07:35:37Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review. Follow-up to BUG-zf4pnj.

Since BUG-zf4pnj's review fix 3, `$('X').item` pairing works only through a lost stamp the source node wrote on its own item (the exact pointer). Everything else — in particular an item behind a routing or pass-through node such as IF, Set, Filter, Merge, or a loop — is refused rather than paired by position, because pairing by position was shown to silently misread another run's item in several shapes (a stamp shared across ports, a stamp shared across runs, a batch held behind a loop).

Refusing is correct given what the pointer currently carries, but it gives up answers that are actually knowable: a pass-through node's item has an exact, traceable origin, it just isn't recorded anywhere the pairing code can read.

# Acceptance Criteria
- [ ] `$('X').item` resolves exactly (not by positional guess) for an item that reached the current node through one or more pass-through nodes (IF, Set, Filter, Merge, a loop) since `X` ran
- [ ] The resolution never reads another run's or another port's item — the failure modes BUG-zf4pnj's review fixes 1-4 caught stay fixed
- [ ] Executions suspended before this change (holding only positional pointers from earlier code) do not silently misread after upgrade — they refuse or pair correctly, never guess

# Implementation Plan

Record the delivering run and position offset on the pending invocation (`pendingInvocation` / `Checkpoint.Pending`), so a held branch or a per-item sub-list carries forward exactly which run and offset it was delivered from, rather than relying on a lost stamp the source node itself wrote. Pairing through a pass-through node then reads that recorded run/offset instead of falling back to a positional guess.

# Notes

Executions suspended across the upgrade may hold positional pointers written by earlier code (predating BUG-zf4pnj's review fix 3). Those must not be read as if they were the new run/offset record.

# Related Files

internal/engine/runner.go — `pointerInto`, `pairNodeItem`, `pendingInvocation`, `Checkpoint.Pending`
internal/expression/roots.go — `NodeItem` and lineage stamping
.pine/tickets/BUG-zf4pnj.md — the ticket that removed the positional fallback and left this as the follow-up (see "Review fix 3" and "the controller's follow-up ticket")
