---
id: BUG-q6b75c
title: '`exec trace` of a finished run stops after 64 events and invents a terminal frame with a backwards id'
status: done
priority: high
labels:
    - cli
    - events
    - debugging
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-10-08T00:00:00Z"
---

# Description

The subscriber queue holds 64 events while the history keeps 256. The replay silently drops the rest (including `node.failed` at the end of a failing run) and still claims to be complete.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-19). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n/a (the CLI and agent surface). `exec trace` is described as "collect an execution's events into one envelope, stopping at its outcome".

# Steps to Reproduce

1. `kilasflow run wf_01a0cbc7-dc58-7ac1-b576-a9614a0b7f6a --input '{"list":[1,…,60]}' --wait` (the execution has 65 node runs). 2. `kilasflow exec trace <id>`. 3. Do the same for the AI execution exec_01a0cbc4-134f-….

# Expected

A trace of a finished execution built from the durable node runs, or a replay that covers the whole retained buffer (256), with a flag when events were lost and monotonic ids.

# Actual

The trace returns exactly 64 retained events plus `{"id":1,"event":"execution.completed"}`, with `"terminal":true,"lastEventId":1`. Everything after event 64 is missing: `done` appears 0 times, and in the AI run the agent's `node.completed` and the final model events are gone. A client resuming with `--from 1` would replay from the start again. For a failing run, the `node.failed` event at the end would be dropped, and the result still claims to be complete.

# Acceptance Criteria
- [x] A trace of a finished execution covers the whole retained history, or is built from the durable node runs
- [x] Lost events are flagged (`lagged`/`truncated`), never silent
- [x] The synthetic terminal frame's id is after the last delivered id

# Implementation Plan

Size the subscriber channel to the replay length (or stream the replay before subscribing), surface `lagged`, and number the synthetic terminal after the last delivered id.

# Notes

Related (from the audit): none

# Related Files

`$SP/agents/ux-debug/cli/trace_e2big.json`, `cli/trace_ai.json`. internal/events/events.go:77 (`defaultSubscriberQueue = 64`, while retained history is 256), :172-199 (replay delivered synchronously into the 64-slot channel), :255-265 (`deliver` drops and sets `lagged`). internal/api/handlers/executions.go:497-499 (synthetic terminal id `resumeFrom(input)+1`).

# Attachments

## Fix

**Root cause.** `Broker.Subscribe` pushed the retained history (up to 256 events) into a channel sized to the live allowance (`SubscriberQueue`, 64). `deliver` drops on a full channel, so everything after the 64th event, the run's outcome included, vanished and only `lagged` was set, which nothing read. `StreamEvents` then saw a replay with no terminal frame, and the gate's durable record supplied one numbered `resumeFrom(input)+1`: id 1 after 64 frames. So `exec trace` ended with `{"id":1,"event":"execution.completed"}` and `terminal: true`, and a client resuming from it replayed the run from the start.

**What changed.**
- `internal/events/events.go`: `Subscribe` sizes the queue to `len(replay) + SubscriberQueue` and fills it with the replay under the broker lock, before the subscription is visible to `Publish`. The whole retained history is replayed, and a live event cannot overtake it. A new `Subscription.Truncated()` reports a replay that starts after a gap (events after the requested id had aged out of the 256-event history).
- `internal/api/handlers/executions.go`: the stream tracks the last id it sent, and the reconstructed terminal frame is numbered `lastSent+1`, never behind a frame already delivered. The frame that ends a stream carries an SSE comment `: truncated: …` when the subscription lagged or was truncated. A reconstructed terminal always carries it, because it stands in for a lost frame. A comment leaves the event schema (and the generated SDK and web types) unchanged, and browsers ignore it.
- `internal/cli/sse.go`, `internal/cli/verbs_exec.go`: the stream reader sets `truncated` on a frame that carries that comment. The `exec trace` envelope gains `truncated`, set by a marked frame or by ids that skip (the first id after `--from`, or any later one). Human mode prints a line pointing at `exec get`.
- `docs/src/content/docs/reference/cli.md`: documents the `truncated` field.

**Tests that prove it.**
- `internal/events`: `TestSubscribeReplaysAHistoryLongerThanTheSubscriberQueue` (100 events with a 4-slot queue replay in order and end on the outcome, neither lagged nor truncated) and `TestSubscribeReportsHistoryThatAgedOutBeforeTheReplay`.
- `internal/api`: `TestAFinishedRunLongerThanTheSubscriberQueueStreamsEveryEvent`. A finished, gated run with 100 events streams ids 1…100 and ends on its own `execution.completed` with no marker. Before the fix it failed with 65 frames: 64 plus an invented id 1. `TestAFinishedExecutionEndsItsStreamEvenWhenTheTerminalFrameWasLost` now also asserts that the reconstructed frame's id is 3 (after the two sent) and that it carries the `truncated` marker.
- `internal/cli`: `TestExecTraceFlagsLostEvents` (contiguous, resumed contiguous, gap, aged out, reconstructed).
- `go test ./internal/events/ ./internal/api/ ./internal/api/handlers/ ./internal/cli/ ./internal/webhook/` passes, `go test -race ./internal/events/` passes, and `go vet` is clean on the touched packages.

**Not covered here.** A subscriber that lags in the *live* loop (it stops reading while a run is still publishing) can still drop the run's terminal frame and then wait on heartbeats. That path is unchanged. It needs a client that stalls for 64+ events, which `exec trace` does not do.
