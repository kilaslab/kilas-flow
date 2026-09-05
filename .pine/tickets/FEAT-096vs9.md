---
id: FEAT-096vs9
title: Give chat memory real session semantics and retention
status: todo
priority: high
labels:
    - ai
    - parity
deps:
    - FEAT-ybm2pd
parent: EPIC-m42s3g
phase: p5
created: "2026-09-05T05:02:01Z"
updated: "2026-09-05T05:02:01Z"
---

## Scope

`memoryNode()` in `nodes/ai.go` declares three parameters: `sessionId`, `maxMessages` (default 40) and `maxAgeMinutes` (default 1440). `executeMemory` resolves all three and writes all three into the `$ai` descriptor. `AgentExecutor.Execute` reads exactly one of them — `sessionId`. Both retention bounds are collected, persisted into the execution record where a reader will reasonably assume they mean something, and then ignored. The retention that actually applies is whatever `cmd/kilasflow/main.go` passed at startup: `ai.NewBufferMemory(ai.Retention{}, nil)`, which normalises to `DefaultRetention()` — 40 messages and 24 hours, identically, for every memory node in every workflow of every tenant.

The store is one process-wide `BufferMemory`. Keys are properly scoped — `ai.SessionKey` carries tenant and workflow and joins the three parts with a NUL byte that cannot appear in an id, so two sessions can never collide into one history. But the map, its mutex and its bounds are shared: one tenant's chat volume is another tenant's memory pressure, and there is no per-tenant ceiling, no eviction beyond the per-session window, and nothing calls `BufferMemory.Forget` when a workflow or a tenant goes away.

The session semantics are the other half. n8n's memory node takes a session key either from the incoming item (`fromInput`) or from an expression (`customKey`), and since v1.4 it auto-scopes the stored key with a `__<node name>` suffix so two memory nodes in one workflow do not silently share a conversation. An import that ignores the suffix merges two distinct conversations into one, which looks like the model losing its mind rather than like a bug.

There is a hard prerequisite in p1-6. `internal/execution/redact.go` carries `session` and `sessionid` on its sensitive-key list and runs at webhook ingest and on every executions write, with the runner rehydrating the trigger item from the redacted record. So an item field named `sessionId` is `[redacted]` by the time the memory node resolves it, and every user in the system collapses into one bucket keyed on that literal string. Nothing in this ticket works until that is fixed.

Postgres-backed chat memory is p6's tier unlock and is deliberately out of scope; the point of keeping `ai.Memory` narrow is that it drops in behind the same interface.

## Acceptance criteria

- [ ] `maxMessages` and `maxAgeMinutes` are enforced per memory node: two memory nodes in one workflow with different bounds retain different amounts.
- [ ] Session key modes match n8n — `fromInput` reading a named field on the incoming item, and `customKey` taking an expression — with the `__<node name>` auto-scoping suffix applied and a documented way to opt out.
- [ ] A tenant cannot read another tenant's conversation even with a correctly guessed session id, proven by a test that tries.
- [ ] Per-tenant memory is bounded by a configured ceiling on retained sessions, oldest evicted first, and the current usage is observable.
- [ ] A conversation survives across separate executions of the same workflow inside the retention window, and is gone after it.
- [ ] Deleting a workflow or a tenant drops its sessions — `BufferMemory.Forget` exists and has no caller today.
- [ ] A memory node saved before this ticket keeps addressing the same conversation it addressed before, or the change of key is reported on import.

## Implementation Plan

Start in `internal/ai/memory.go`. Retention is currently a constructor argument, so making it per-node means either widening the `Memory` interface or recording bounds per key. Recommend the latter shape: leave `Memory.Load` and `Memory.Append` as they are and have the agent executor pass a session policy alongside the key, so a durable implementation in p6 receives the bounds rather than re-deriving them from a node it cannot see. `prune` already applies age before count, which is the right order — a long-idle session should be emptied, not merely trimmed — so the change is about where the numbers come from, not how they are applied.

Then `nodes/ai.go`: `memoryNode()` gains the session mode parameters, `executeMemory` writes the resolved key and the bounds into the descriptor as it already does, and `AgentExecutor.Execute` stops reading only `sessionId`. Then `cmd/kilasflow/main.go` for the per-tenant ceiling, which is a configuration value and belongs beside the other deployment decisions there.

The trap is the redaction path. The session id arrives on an item that has been through `internal/execution/redact.go`, and the runner rehydrates the trigger item from the persisted, redacted record. Do not build a session key from a value read back out of storage; take it from the live item, and write a test that runs a full webhook-triggered execution rather than only a unit test of the memory store, because a unit test cannot see that boundary at all.

Second trap: the auto-scoping suffix is part of the stored key, so turning it on changes where every existing conversation lives. Decide and document whether existing keys are migrated or simply start fresh — recommend starting fresh with the change noted in the release evidence, since a chat window is a working buffer and not a record of account.

## References

- Plan `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, p5 entry V2-p5-5, p1 entry V2-p1-6 (redaction), p6 entry V2-p6-6 for the durable tier.
- `nodes/ai.go` (`memoryNode`, `executeMemory`, `AgentExecutor.Execute`), `internal/ai/memory.go` (`Retention`, `BufferMemory`, `prune`, `Forget`), `internal/ai/ai.go` (`SessionKey`, `Memory`), `cmd/kilasflow/main.go`, `internal/execution/redact.go`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 06 — the Simple Memory parameters. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
