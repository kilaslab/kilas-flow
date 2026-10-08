---
id: BUG-w9k234
title: a whole-row condition expression on a Data table step node lets the incoming item choose keyName and operator
status: done
priority: medium
labels:
    - datastore
    - security
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-10-08T15:58:01Z"
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
- [x] A condition row in `filters.conditions` written as a single expression marker is refused at save, consistent with the function's existing comment
- [x] `datastoreToolFrom` re-runs `checkDatastoreToolOperation` on the descriptor it is handed before building the callable tool
- [x] A test covers a whole-row condition expression being refused, and a tool descriptor reaching `datastoreToolFrom` without prior validation being checked there

# Related Files

nodes/datastore.go `refuseDatastoreColumnExpressions`, ~341-369
nodes/datastore.go `datastoreFilterRows`, ~371 onward (reads `filters.conditions`)
nodes/ai.go `datastoreToolFrom`, ~3067
nodes/datastore.go `checkDatastoreToolOperation`, ~1068

## Fix

Root cause: an expression marker is itself a `map[string]any`, so `datastoreFilterRows` read a whole-row marker as a condition row with no `keyName` and no `condition`. Validation then passed it as an equality, and at run `expression.Resolve` replaced it with whatever row the item spelled. Repro: a Delete step whose one row was `{{ ({ keyName: $json.column, condition: $json.op, keyValue: $json.value }) }}`, fed `{"column":"title","op":"neq","value":"keep"}`, deleted every row except `keep`. An operator written as an expression (`condition: {{ $json.op }}`) did the same through the operator alone.

- **Step node** (`datastoreFilterRows`, which both the save rule and the pre-resolve run backstop `refuseDatastoreColumnExpressions` call): a row that is a marker is refused, and the check comes before the shape check. The message names `filters.conditions[i]` and points the author at a literal column and condition with the expression in `keyValue`. A condition (operator) marker is refused as `filters.conditions[i].condition`. This goes slightly past the ticket's first criterion, but the ticket names the operator as part of the same hole: refusing only the whole row would leave the operator open, and the tool already treats it as structure. The importer is unaffected, because an n8n operator expression already arrives as `eq` with a lossy diagnostic. The doc comment now lists what the function refuses. `keyValue` expressions still work; the test proves the same attacker body deletes only the row its value names.
- **Agent tool build** (`datastoreToolFrom`): it re-runs `checkDatastoreToolOperation` on a copy of the descriptor's parameters, pinned to the descriptor's resolved `datastoreId` (the table every call uses, which also lets a legacy parameterless get descriptor pass). It then requires `DatastoreToolOperation` of those parameters to equal the descriptor's `operation`, because a write claimed over a read's parameters would skip the write's structure rules. Every descriptor the tool executor builds already satisfies both checks.
- Skill docs (`kilasflow-datastore` SKILL.md, references/FILTERS.md) now say the operator and a whole condition row are never expressions. `make generate-skills-index-check` reports the index up to date.

Proof:
- `TestDatastoreConditionTakesItsColumnAndOperatorFromTheNode`: the whole row and the operator are refused at Compile and at Execute with rows untouched, and a `keyValue` expression deletes exactly one row.
- `TestDatastoreToolDescriptorIsCheckedWhereTheAgentBuildsIt`: three hand-built descriptors handed straight to the agent are refused where the tool is built, and nothing is written. One has `match` from `$json`, which only the tool rules catch. One is a whole-row marker on a delete. One is an `insert` whose parameters perform a get. Before the fix, two of the three wrote rows.
- `go test ./nodes/ ./internal/interop/... ./internal/datastore/...` → ok; `go vet ./nodes/` → clean.
