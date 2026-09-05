---
id: FEAT-pnbt4z
title: Resolve expressions in the Set, IF and Merge nodes
status: todo
priority: high
created: "2026-09-05T11:40:26Z"
updated: "2026-09-05T11:40:26Z"
parent: EPIC-m42s3g
phase: p4
---

## Scope

`executeSet`, `executeIF` and `executeMerge` in `nodes/executors.go` read `node.Parameters` directly and take `engine.Request` as `_`. They never call `expression.Resolve`, so a `{"mode":"expression","value":"…"}` marker is treated as data.

The consequence in a Set node is the worst kind: the marker object is written into the item verbatim. A workflow that assigns `chatId = {{ $json.message.chat.id }}` produces an item whose `chatId` is `{"mode":"expression","value":"{{ $json.message.chat.id }}"}` — and everything downstream sends that object. No error, no diagnostic; the workflow runs and the data is wrong.

In an IF node it is quieter and just as bad: a condition whose value is an expression compares against the marker, so the branch is decided by a comparison nobody wrote.

Every other executor that reads parameters — HTTP, the AI family, the database nodes, the webhook responder — resolves them. These three were missed, and the n8n importer translates n8n's `=`-prefixed strings into exactly the marker form they ignore, so **every imported workflow using an expression in a Set or an IF is affected**.

Found while adding the Telegram action node (FEAT-6vfn3s), which is how a Set node between a trigger and a reply came to be looked at closely.

## Acceptance criteria

- [ ] Set, IF and Merge resolve their parameters through `expression.Resolve` per item, with `request.ExpressionContext`, exactly as the HTTP node does.
- [ ] An assignment whose value is an expression writes the resolved value, proven by a test that runs a Set node reading `$json` and asserts the item, not the marker.
- [ ] An IF condition whose value is an expression compares the resolved value, proven by a test where the two branches would differ.
- [ ] Per item, not per node: an expression in a Set assignment resolves against the item it is being written onto, so a two-item batch gets two different values.
- [ ] A corpus rescore records what this moves, since several fixtures fail on Set today.

## Implementation Plan

The pattern is already in `nodes/http.go`: resolve inside the per-item loop with `expressionContext(item, input, request, index)`. Move each of the three onto it rather than resolving once outside the loop — resolving once is what makes a two-item batch write the same value twice, which is a subtler version of the same bug.

Merge is the odd one: it has no per-item parameters worth resolving today, so the change there may be nothing more than proving that with a test. Do not add a resolve call it does not need.

The trap is `assignments`. Its values are arbitrary — a string, a number, a nested object — and `expression.Resolve` walks a parameter tree, so passing the whole `Parameters` map through it is right and hand-walking `assignments` is not.

## References

- `nodes/executors.go` — `executeSet`, `executeIF`, `executeMerge`, all three taking `_ engine.Request`.
- `nodes/http.go` — `Execute`'s per-item `expression.Resolve` call, the pattern to follow.
- `internal/interop/n8n/parameters.go` — `fromN8NValue`, which produces the marker these ignore.
- `.pine/tickets/FEAT-6vfn3s.md` — where this was found.
