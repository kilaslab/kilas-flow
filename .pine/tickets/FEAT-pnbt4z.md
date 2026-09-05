---
id: FEAT-pnbt4z
title: Resolve expressions in the Set, IF and Merge nodes
status: done
priority: high
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T11:40:26Z"
updated: "2026-09-05T11:43:17Z"
---

## Scope

`executeSet`, `executeIF` and `executeMerge` in `nodes/executors.go` read `node.Parameters` directly and take `engine.Request` as `_`. They never call `expression.Resolve`, so a `{"mode":"expression","value":"…"}` marker is treated as data.

The consequence in a Set node is the worst kind: the marker object is written into the item verbatim. A workflow that assigns `chatId = {{ $json.message.chat.id }}` produces an item whose `chatId` is `{"mode":"expression","value":"{{ $json.message.chat.id }}"}` — and everything downstream sends that object. No error, no diagnostic; the workflow runs and the data is wrong.

In an IF node it is quieter and just as bad: a condition whose value is an expression compares against the marker, so the branch is decided by a comparison nobody wrote.

Every other executor that reads parameters — HTTP, the AI family, the database nodes, the webhook responder — resolves them. These three were missed, and the n8n importer translates n8n's `=`-prefixed strings into exactly the marker form they ignore, so **every imported workflow using an expression in a Set or an IF is affected**.

Found while adding the Telegram action node (FEAT-6vfn3s), which is how a Set node between a trigger and a reply came to be looked at closely.

## Acceptance criteria

- [x] Set, IF and Merge resolve their parameters through `expression.Resolve` per item, with `request.ExpressionContext`, exactly as the HTTP node does. **Merge deliberately does not** — see below.
- [x] An assignment whose value is an expression writes the resolved value, proven by a test that runs a Set node reading `$json` and asserts the item, not the marker.
- [x] An IF condition whose value is an expression compares the resolved value, proven by a test where the two branches would differ.
- [x] Per item, not per node: an expression in a Set assignment resolves against the item it is being written onto, so a two-item batch gets two different values.
- [x] A corpus rescore records what this moves, since several fixtures fail on Set today.

## Outcome

Set and IF now resolve their whole parameter tree per item, through the same `expressionContext` the HTTP node uses. `TestSetResolvesAnExpressionAssignmentPerItem` asserts both halves of the fix: that `Hello {{ $json.name }}` writes *Hello Ada* and *Hello Grace* rather than the marker, and that the two items get **different** values — resolving once outside the loop would have written the first item's value onto both, which is the subtler version of the same bug and would have passed a single-item test.

IF gained a second thing worth keeping: the condition is validated once before any item and resolved per item after. A malformed condition list is one error rather than one per row, and a condition reading `{{ $json.wanted }}` now parts a batch instead of sending every item down the same branch.

**Merge deliberately resolves nothing.** Its only parameter is a mode chosen from a fixed list, and an expression there would name a mode that depends on the data — not a thing this node offers. The acceptance criterion asked for all three; adding a resolve call Merge does not need would be a call nobody could explain, so it carries a comment saying why instead.

**The corpus did not move**, and that is the honest result: 12 of 39 activatable before and after. Nothing here changes whether a workflow *compiles* — the fixtures that fail on Set fail with `assignments must be a non-empty object`, which is n8n's v3 assignment shape and belongs to FEAT-xqqjqv. What this fixes is silent wrongness at run time in workflows that already compile, which no tier in the scoreboard measures. That is worth saying plainly rather than hunting for a number that moved.

## Implementation Plan

The pattern is already in `nodes/http.go`: resolve inside the per-item loop with `expressionContext(item, input, request, index)`. Move each of the three onto it rather than resolving once outside the loop — resolving once is what makes a two-item batch write the same value twice, which is a subtler version of the same bug.

Merge is the odd one: it has no per-item parameters worth resolving today, so the change there may be nothing more than proving that with a test. Do not add a resolve call it does not need.

The trap is `assignments`. Its values are arbitrary — a string, a number, a nested object — and `expression.Resolve` walks a parameter tree, so passing the whole `Parameters` map through it is right and hand-walking `assignments` is not.

## References

- `nodes/executors.go` — `executeSet`, `executeIF`, `executeMerge`, all three taking `_ engine.Request`.
- `nodes/http.go` — `Execute`'s per-item `expression.Resolve` call, the pattern to follow.
- `internal/interop/n8n/parameters.go` — `fromN8NValue`, which produces the marker these ignore.
- `.pine/tickets/FEAT-6vfn3s.md` — where this was found.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `21056a16` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `109ff8f6` — feat(packs): add the Telegram action node at n8n parity
- Files changed (base → working tree):

```
 .pine/tickets/FEAT-6vfn3s.md                   |  354 +++++++-
 .pine/tickets/FEAT-pnbt4z.md                   |   55 ++
 cmd/kilasflow/main.go                          |    4 +
 internal/credentials/builtin.go                |   15 +-
 internal/credentials/credentials.go            |    7 +
 internal/credentials/registry.go               |   30 +-
 internal/engine/runner.go                      |   30 +-
 internal/engine/runner_test.go                 |   87 ++
 internal/interop/n8n/corpus/scoreboard_test.go |    4 +
 internal/interop/n8n/n8n.go                    |   15 +
 internal/interop/n8n/n8n_test.go               |  102 +++
 internal/interop/n8n/parameters.go             |   23 +
 internal/routing/executor.go                   |  216 ++++-
 internal/routing/request.go                    |   34 +-
 internal/routing/response.go                   |    5 +
 internal/routing/routing.go                    |   29 +-
 nodes/executors.go                             |   46 +-
 nodes/executors_test.go                        |  104 +++
 packs/telegram/README.md                       |   40 +
 packs/telegram/pack.json                       | 1119 ++++++++++++++++++++++++
 packs/telegram/telegram.go                     |   58 ++
 packs/telegram/telegram_test.go                |  466 ++++++++++
 web/src/lib/api/generated/models/field.ts      |    1 +
 23 files changed, 2793 insertions(+), 51 deletions(-)
```
