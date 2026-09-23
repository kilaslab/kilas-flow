---
id: BUG-w9k234
title: a whole-row condition expression on a Data table step node lets the incoming item choose keyName and operator
status: todo
priority: medium
labels:
    - datastore
    - security
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-09-23T07:35:37Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

`refuseDatastoreColumnExpressions` (nodes/datastore.go ~341-369) refuses an expression written at `filters` itself, at `counterColumn`, at `columns`, and at `columns.matchingColumns` — but it does not check a condition *row* inside `filters.conditions` that is itself written as one marker (a single expression standing in for the whole `{keyName, condition, keyValue}` object), rather than an expression only inside `keyValue`. The function's own comment says mapper-value markers are refused; the code doesn't do that for a whole-row marker.

Separately, `datastoreToolFrom` (nodes/ai.go ~3067) builds a callable tool from a datastore descriptor without re-running `checkDatastoreToolOperation` on the descriptor it is handed, so a descriptor that reaches it by a path other than the one that already validates it skips the check entirely.

Together, an incoming item (e.g. from a webhook) can choose which column (`keyName`) and which comparison (`condition`/operator) a Data table step reads or writes, not just the value being compared — the same class of problem `refuseDatastoreColumnExpressions` exists to prevent for `columns`.

# Steps to Reproduce

1. Configure a Data table step node's `filters.conditions` with one row written as a single expression (rather than an expression only at `keyValue`) that resolves from incoming item data.
2. Send a webhook payload that supplies a `keyName`/`condition` pair of the attacker's choosing.
3. Observe the step reads or writes using that attacker-chosen column and operator.

# Expected

A whole-row condition marker is refused at save time, the same way `filters` itself, `counterColumn`, and `columns` already are. A datastore tool descriptor is re-checked with `checkDatastoreToolOperation` wherever `datastoreToolFrom` builds a callable tool from it, regardless of how the descriptor arrived.

# Actual

`refuseDatastoreColumnExpressions` only inspects `columns` and `keyName`-shaped fields directly; a condition row written as one marker passes through unexamined. `datastoreToolFrom` does not re-validate the descriptor it is handed.

# Acceptance Criteria
- [ ] A condition row in `filters.conditions` written as a single expression marker is refused at save, consistent with the function's existing comment
- [ ] `datastoreToolFrom` re-runs `checkDatastoreToolOperation` on the descriptor it is handed before building the callable tool
- [ ] A test covers a whole-row condition expression being refused, and a tool descriptor reaching `datastoreToolFrom` without prior validation being checked there

# Related Files

nodes/datastore.go `refuseDatastoreColumnExpressions`, ~341-369
nodes/datastore.go `datastoreFilterRows`, ~371 onward (reads `filters.conditions`)
nodes/ai.go `datastoreToolFrom`, ~3067
nodes/datastore.go `checkDatastoreToolOperation`, ~1068
