---
id: BUG-q6b75c
title: '`exec trace` of a finished run stops after 64 events and invents a terminal frame with a backwards id'
status: todo
priority: high
labels:
    - cli
    - events
    - debugging
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
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
- [ ] A trace of a finished execution covers the whole retained history, or is built from the durable node runs
- [ ] Lost events are flagged (`lagged`/`truncated`), never silent
- [ ] The synthetic terminal frame's id is after the last delivered id

# Implementation Plan

Size the subscriber channel to the replay length (or stream the replay before subscribing), surface `lagged`, and number the synthetic terminal after the last delivered id.

# Notes

Related (from the audit): none

# Related Files

`$SP/agents/ux-debug/cli/trace_e2big.json`, `cli/trace_ai.json`. internal/events/events.go:77 (`defaultSubscriberQueue = 64`, while retained history is 256), :172-199 (replay delivered synchronously into the 64-slot channel), :255-265 (`deliver` drops and sets `lagged`). internal/api/handlers/executions.go:497-499 (synthetic terminal id `resumeFrom(input)+1`).

# Attachments
