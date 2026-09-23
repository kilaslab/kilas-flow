---
id: BUG-bw2zc1
title: Wait inside Loop Over Items fails on the 2nd iteration ("suspended node already completed")
status: done
priority: high
labels:
    - engine
    - regression
    - loops
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T04:18:03Z"
---

# Description

Loop → HTTP → Wait → back to the loop is n8n's standard rate-limit pattern. With a timer Wait, the run fails after the first batch, and the failed execution still shows every node green. This is a regression, or a partial fix, of BUG-ysvmaa.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-5). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Loop → HTTP → Wait → back to the loop is the standard rate-limit pattern. Every batch waits and is processed, and a failure marks the failing node red.

**Correction (2026-09-23, on fixing):** the fault is not specific to timers. Every Wait mode fails the same way (interval, until, webhook and approval), and so does a per-item Wait (a Wait set to continue on failure, which resolves one item at a time) that suspends on more than one item. `Resume` refused whenever `checkpoint.Completed` held the suspended node, and that map keeps a node's latest output forever, so from the node's second suspension on the refusal always fired. BUG-ysvmaa's approval-mode test (`TestResumeInsideALoopProcessesEveryBatch`) passed only because its fake, `waitSuspendOnce`, suspends once and passes every later batch straight through, whereas a real Wait suspends on every batch.

# Steps to Reproduce

1. `kilasflow run wf_01a0cbc2-0b24-7278-8aa1-837b331ee14b --wait` (5 items, batch size 1, Wait 2 s, Set, back to the loop). 2. Also click Execute in the editor. 3. Open the execution.

# Expected

All five batches processed with a 2 s pause each, as n8n does. On any engine failure, the node that failed should be marked.

# Actual

Both runs fail after about 4 s with `suspended node "wait" already completed` (code `execution.failed`). The trace ends at `loop-over-items run 1` dispatching item 2, and no failed node run is recorded. The replay shows the execution as Failed while every node, including Wait, is green "Succeeded". The error names no node, and items 2 to 5 are never processed.

# Acceptance Criteria
- [x] A Loop Over Items (batch 1, 5 items) with a 2 s timer Wait processes all 5 batches
- [x] Resume checks completion by run index, or clears the Wait from `Completed` when the loop re-enters its body
- [x] When a resume fails, a failed node run is recorded for the suspended node, so the replay marks it red
- [x] A test for timer-mode resume inside a loop sits next to the existing approval-mode one

# Implementation Plan

Key the completion check in Resume by run index, or clear the Wait node from `Completed` when the loop re-enters its body. Add a timer-mode resume-inside-loop test next to the approval-mode one. When a resume fails, write a failed node run for the suspended node.

# Notes

Related tickets: BUG-ysvmaa

This affects every Wait mode, not only timers, and also a per-item Wait that suspends on more than one item. The old approval-mode loop test passed only because its fake suspended once. See the correction under Description.

Related (from the audit): BUG-ysvmaa (done; "A Wait inside a loop…" is claimed fixed with an approval-token test; with a timer Wait it still fails, so this is a regression or a partial fix)

# Related Files

`$SP/agents/ux-debug/cli/run_e.json`, `e-01-running-canvas.png` (editor banner "Run failed: suspended node "wait" already completed" after the first iteration), `e-03-exec-loop.png`. internal/engine/runner.go:889-891 (Resume refuses when `checkpoint.Completed` already holds the Wait node from iteration 1).

# Attachments

## Progress 2026-09-23 (the fix)

The completion check in `Resume` is now keyed by run index. `Checkpoint` gains an optional `SuspendRun` (checkpoint version stays 1), which is how many runs of the suspending node had completed when it suspended. `snapshotCheckpoint` sets it from `len(state.runs[node])`, and `Resume` refuses only when `len(Runs[SuspendNode]) > SuspendRun`, meaning the suspended run itself is already recorded. Checkpoints written before the field existed read 0. That is exact for a first suspension, and it refuses every later one as before, but the refusal is now recorded.

A refused resume (either refusal after the node is found: already completed, or the resume output's stream count) now returns a `Result` holding one failed `NodeRun` for the suspended node. It carries the checkpoint input, `SuspendAttempt`, `RunIndex = len(Runs[SuspendNode])`, code `node.failed` and the refusal. `runOnce` already writes a failed run's rows before settling it as failed, so the replay marks the Wait red instead of showing every node green.

Tests in `internal/engine/wait_service_test.go`, next to the approval-mode one:
- `TestATimerWaitInsideALoopProcessesEveryBatch`: 5 items, batch 1, a 2 s interval wait on every batch, driven by the test clock and timer stub. Asserts 5 body runs, all 5 items reach `done` once each, 5 succeeded wait runs at run indexes 0 to 4, and a succeeded execution.
- `TestAnApprovalWaitInsideALoopProcessesEveryBatch`: the approval suspender fires on every call.
- `TestAPerItemWaitThatSuspendsOnTwoItemsProcessesBoth`: a per-item wait that suspends on both of its items.
- `TestAResumeThatFailsRecordsAFailedRunOnTheWait`: a second-batch checkpoint rewritten without `suspendRun` is still refused, and the trace ends on one failed run of the wait (run index 1, attempt 1).

RED: all four failed before the change with `suspended node "hold" already completed`, or for the fourth, the missing `suspendRun`. With the failed run dropped from `failedResume`, the fourth fails with "the trace holds 0 failed runs". GREEN: `go test ./internal/engine/ -count=1` passes, and the six loop and per-item resume tests pass `-race -count=3`.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (2):
  - `77c159c0` — BUG-bw2zc1: a Wait inside a loop resumes on every batch, and a refused resume marks the Wait failed
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          | Bin 0 -> 119127 bytes
 .pine/memory/code-node.md                          |   3 +-
 .pine/memory/licensing.md                          |   2 +-
 .pine/memory/n8n-reference.md                      |   4 +-
 .pine/roadmap.md                                   |   8 +-
 .pine/tickets/BUG-0xv7bg.md                        |  54 +++
 .pine/tickets/BUG-15st2k.md                        | 162 +++++++
 .pine/tickets/BUG-2eryxn.md                        |  49 ++
 .pine/tickets/BUG-2mes2k.md                        |  54 +++
 .pine/tickets/BUG-2n4rfz.md                        |  51 +++
 .pine/tickets/BUG-2z8geh.md                        |  52 +++
 .pine/tickets/BUG-3k12ky.md                        | 163 +++++++
 .pine/tickets/BUG-3qxx0j.md                        |  59 +++
 .pine/tickets/BUG-56qqgx.md                        |  52 +++
 .pine/tickets/BUG-5bgx5c.md                        |  59 +++
 .pine/tickets/BUG-605n21.md                        | 131 ++++++
 .pine/tickets/BUG-66fhea.md                        |  97 ++++
 .pine/tickets/BUG-6d6wbg.md                        |  98 ++++
 .pine/tickets/BUG-6gkd12.md                        | 107 +++++
 .pine/tickets/BUG-719gaz.md                        |  88 ++++
 .pine/tickets/BUG-9dw5me.md                        |  53 +++
 .pine/tickets/BUG-9pmv8y.md                        |  61 +++
 .pine/tickets/BUG-b3p8va.md                        |  61 +++
 .pine/tickets/BUG-b4cb1c.md                        | 119 +++++
 .pine/tickets/BUG-b8bwhw.md                        |  55 +++
 .pine/tickets/BUG-bcahaj.md                        | 100 +++++
 .pine/tickets/BUG-bw2zc1.md                        |  73 +++
 .pine/tickets/BUG-dstsg9.md                        |  54 +++
 .pine/tickets/BUG-e7dwpk.md                        |  51 +++
 .pine/tickets/BUG-ecbq28.md                        | 111 +++++
 .pine/tickets/BUG-epy2se.md                        | 122 +++++
 .pine/tickets/BUG-g7ffj1.md                        |  50 +++
 .pine/tickets/BUG-hmp85t.md                        |  99 +++++
 .pine/tickets/BUG-j7qrp2.md                        |  52 +++
 .pine/tickets/BUG-mzk0xn.md                        |  35 ++
 .pine/tickets/BUG-n6p7qy.md                        | 101 +++++
 .pine/tickets/BUG-n9a6bz.md                        |  54 +++
 .pine/tickets/BUG-namghh.md                        |  48 ++
 .pine/tickets/BUG-nbymq4.md                        |  52 +++
 .pine/tickets/BUG-ngt25j.md                        |  52 +++
 .pine/tickets/BUG-nn74ph.md                        |  51 +++
 .pine/tickets/BUG-nzy3pa.md                        | 186 ++++++++
 .pine/tickets/BUG-p334yw.md                        |  53 +++
 .pine/tickets/BUG-p3j233.md                        |  53 +++
 .pine/tickets/BUG-phv0r9.md                        |  56 +++
 .pine/tickets/BUG-ppvyzr.md                        |  58 +++
 .pine/tickets/BUG-pzkpfr.md                        |  53 +++
 .pine/tickets/BUG-q6b75c.md                        |  52 +++
 .pine/tickets/BUG-r1m83f.md                        |  52 +++
 .pine/tickets/BUG-rbask0.md                        | 140 ++++++
 .pine/tickets/BUG-rh7mpa.md                        |  52 +++
 .pine/tickets/BUG-rs0xq1.md                        |  46 ++
 .pine/tickets/BUG-rytwy7.md                        |  55 +++
 .pine/tickets/BUG-sgrxhh.md                        |  53 +++
 .pine/tickets/BUG-t12ffz.md                        |  57 +++
 .pine/tickets/BUG-t3p92b.md                        |  94 ++++
 .pine/tickets/BUG-txafja.md                        |  55 +++
 .pine/tickets/BUG-v8ksv8.md                        |  49 ++
 .pine/tickets/BUG-vsmnby.md                        |  36 ++
 .pine/tickets/BUG-x28fsx.md                        | 142 ++++++
 .pine/tickets/BUG-x6gyc1.md                        |  54 +++
 .pine/tickets/BUG-xam6t8.md                        | 179 ++++++++
 .pine/tickets/BUG-y38bss.md                        |  54 +++
 .pine/tickets/BUG-ywbvfa.md                        |  36 ++
 .pine/tickets/BUG-z0s4zg.md                        | 100 +++++
 .pine/tickets/BUG-zf4pnj.md                        |  55 +++
 .pine/tickets/EPIC-3en6xr.md                       |  88 ++++
 .pine/tickets/EPIC-62zt4j.md                       | 110 +++++
 .pine/tickets/EPIC-7c3ry9.md                       |  44 ++
 .pine/tickets/EPIC-8rbys7.md                       | 192 ++++++++
 .pine/tickets/EPIC-m42s3g.md                       |   2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 478 ++++++++++++++++++++
 .pine/tickets/FEAT-02cj1g.md                       | 102 +++++
 .pine/tickets/FEAT-02zdcq.md                       | 169 +++++++
 .pine/tickets/FEAT-0hdfzd.md                       | 106 +++++
 .pine/tickets/FEAT-0xsc1s.md                       |  35 ++
 .pine/tickets/FEAT-1ge0xc.md                       |  31 ++
 .pine/tickets/FEAT-1mxtsn.md                       | 104 +++++
 .pine/tickets/FEAT-274c4p.md                       |  68 +++
 .pine/tickets/FEAT-27g2za.md                       |  33 ++
 .pine/tickets/FEAT-2kx0hx.md                       | 260 +++++++++++
 .pine/tickets/FEAT-2m24nh.md                       |  53 +++
 .pine/tickets/FEAT-2m4yvz.md                       | 101 +++++
 .pine/tickets/FEAT-38je8w.md                       |  35 ++
 .pine/tickets/FEAT-39ttf6.md                       |  32 ++
 .pine/tickets/FEAT-3t112f.md                       |  53 +++
 .pine/tickets/FEAT-3ykb4v.md                       |  37 ++
 .pine/tickets/FEAT-4bjfny.md                       | 100 +++++
 .pine/tickets/FEAT-4bvcrb.md                       |  38 ++
 .pine/tickets/FEAT-4e376e.md                       |  56 +++
 .pine/tickets/FEAT-4jhtny.md                       |  30 ++
 .pine/tickets/FEAT-4pz9fn.md                       |  37 ++
 .pine/tickets/FEAT-53pa9a.md                       |  52 +++
 .pine/tickets/FEAT-5fx926.md                       |  57 +++
 .pine/tickets/FEAT-5g42rz.md                       |  30 ++
 .pine/tickets/FEAT-5kv1jq.md                       |   8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |  99 +++++
 .pine/tickets/FEAT-6m295t.md                       |  38 ++
 .pine/tickets/FEAT-6qzza1.md                       |  58 +++
 .pine/tickets/FEAT-6r663e.md                       |  32 ++
 .pine/tickets/FEAT-70j6dn.md                       |  55 +++
 .pine/tickets/FEAT-7cg0cd.md                       |   6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  40 ++
 .pine/tickets/FEAT-7t0xks.md                       |  31 ++
 .pine/tickets/FEAT-8752vx.md                       |  54 +++
 .pine/tickets/FEAT-8zgwp6.md                       |  32 ++
 .pine/tickets/FEAT-9ep5pw.md                       |  31 ++
 .pine/tickets/FEAT-a3dwj2.md                       |  52 +++
 .pine/tickets/FEAT-afkx3k.md                       |  37 ++
 .pine/tickets/FEAT-bfrkyk.md                       |  54 +++
 .pine/tickets/FEAT-c81kp3.md                       |  59 +++
 .pine/tickets/FEAT-cgm1y3.md                       |   2 +-
 .pine/tickets/FEAT-csqgg5.md                       |   6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |  59 +++
 .pine/tickets/FEAT-edzr73.md                       | 121 +++++
 .pine/tickets/FEAT-egm8bf.md                       |  37 ++
 .pine/tickets/FEAT-eqzpzq.md                       | 136 ++++++
 .pine/tickets/FEAT-ez6xtm.md                       |  55 +++
 .pine/tickets/FEAT-f045nj.md                       | 131 ++++++
 .pine/tickets/FEAT-f3hx3a.md                       |  37 ++
 .pine/tickets/FEAT-fpqg78.md                       |  52 +++
 .pine/tickets/FEAT-fqmh01.md                       |  97 ++++
 .pine/tickets/FEAT-fs3pjr.md                       | 205 +++++++++
 .pine/tickets/FEAT-gzd32h.md                       |  31 ++
 .pine/tickets/FEAT-hxztwz.md                       |  37 ++
 .pine/tickets/FEAT-je4f4t.md                       |   4 +-
 .pine/tickets/FEAT-jwhdsy.md                       |   2 +-
 .pine/tickets/FEAT-kcdrcy.md                       | 130 ++++++
 .pine/tickets/FEAT-kfmq1z.md                       |  53 +++
 .pine/tickets/FEAT-kpn0m3.md                       |  37 ++
 .pine/tickets/FEAT-ktasef.md                       | 103 +++++
 .pine/tickets/FEAT-ky75b5.md                       |  52 +++
 .pine/tickets/FEAT-m1fdn4.md                       |  56 +++
 .pine/tickets/FEAT-m7aw75.md                       |  54 +++
 .pine/tickets/FEAT-mammrz.md                       |  35 ++
 .pine/tickets/FEAT-mccadj.md                       |  38 ++
 .pine/tickets/FEAT-mh4e8g.md                       |  32 ++
 .pine/tickets/FEAT-mj2nek.md                       |  98 ++++
 .pine/tickets/FEAT-mngmn1.md                       |  32 ++
 .pine/tickets/FEAT-mq412g.md                       |  58 +++
 .pine/tickets/FEAT-mxmjt7.md                       | 129 ++++++
 .pine/tickets/FEAT-n010f0.md                       |  33 ++
 .pine/tickets/FEAT-n12211.md                       |  34 ++
 .pine/tickets/FEAT-nch9dg.md                       |   6 +-
 .pine/tickets/FEAT-npc3ge.md                       |  37 ++
 .pine/tickets/FEAT-nq1vsx.md                       |  53 +++
 .pine/tickets/FEAT-p01rcw.md                       |  98 ++++
 .pine/tickets/FEAT-p75n7j.md                       |  38 ++
 .pine/tickets/FEAT-pfwjzk.md                       |  30 ++
 .pine/tickets/FEAT-ppnetz.md                       | 141 ++++++
 .pine/tickets/FEAT-pqnxx4.md                       |  37 ++
 .pine/tickets/FEAT-prw1hw.md                       |  56 +++
 .pine/tickets/FEAT-pt6ge9.md                       |  34 ++
 .pine/tickets/FEAT-pxcbqj.md                       |  39 ++
 .pine/tickets/FEAT-q81bq4.md                       |   2 +-
 .pine/tickets/FEAT-qf0hsa.md                       |  53 +++
 .pine/tickets/FEAT-r267jj.md                       |  35 ++
 .pine/tickets/FEAT-r8ph93.md                       |  38 ++
 .pine/tickets/FEAT-rdfjh1.md                       |  32 ++
 .pine/tickets/FEAT-re138f.md                       |  54 +++
 .pine/tickets/FEAT-rkj8ry.md                       |  37 ++
 .pine/tickets/FEAT-s3sfx5.md                       |  31 ++
 .pine/tickets/FEAT-s99vdp.md                       | 155 +++++++
 .pine/tickets/FEAT-sc3qrq.md                       |  54 +++
 .pine/tickets/FEAT-sz4ddp.md                       |  57 +++
 .pine/tickets/FEAT-t26rt7.md                       |   2 +-
 .pine/tickets/FEAT-t38djq.md                       |  56 +++
 .pine/tickets/FEAT-t58m89.md                       |  32 ++
 .pine/tickets/FEAT-t672pv.md                       |  57 +++
 .pine/tickets/FEAT-tjcr13.md                       |  52 +++
 .pine/tickets/FEAT-v2nenc.md                       |  58 +++
 .pine/tickets/FEAT-vjjs8t.md                       |  36 ++
 .pine/tickets/FEAT-vntngh.md                       |  64 +++
 .pine/tickets/FEAT-vvwpjw.md                       |   2 +-
 .pine/tickets/FEAT-w7n7x6.md                       | 131 ++++++
 .pine/tickets/FEAT-w9kqeg.md                       |  10 +-
 .pine/tickets/FEAT-wcr6en.md                       |  52 +++
 .pine/tickets/FEAT-wzfz3d.md                       |  59 +++
 .pine/tickets/FEAT-x9gq0s.md                       |  37 ++
 .pine/tickets/FEAT-xj5tv6.md                       |  38 ++
 .pine/tickets/FEAT-xr75b9.md                       |  58 +++
 .pine/tickets/FEAT-xzdn35.md                       |  56 +++
 .pine/tickets/FEAT-ybm2pd.md                       |   2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |  32 ++
 .pine/tickets/FEAT-ys734v.md                       |  36 ++
 .pine/tickets/FEAT-yxhgeh.md                       |  38 ++
 .pine/tickets/FEAT-yyjfjq.md                       |   2 +-
 .pine/tickets/FEAT-z90r5a.md                       |  32 ++
 .pine/tickets/FEAT-zhdxc4.md                       |  38 ++
 .pine/tickets/FEAT-zjrw76.md                       |  37 ++
 .pine/tickets/FEAT-zm3wh2.md                       |  99 +++++
 .pine/tickets/FEAT-zn5rqy.md                       | 103 +++++
 .pine/tickets/FEAT-zwpvbf.md                       |  60 +++
 CHANGELOG.md                                       |  25 ++
 CONTRIBUTING.md                                    |  22 +
 README.md                                          | 450 +++++--------------
 config.example.yaml                                |   8 +-
 docs/src/content/docs/concepts/architecture.md     |  84 ++++
 docs/src/content/docs/concepts/execution-model.md  |  15 +-
 docs/src/content/docs/concepts/node-registry.md    |   5 +-
 .../src/content/docs/concepts/safety-boundaries.md |   2 +-
 docs/src/content/docs/concepts/webhooks.md         |  33 ++
 .../content/docs/operate/acceptance-capstone.md    |   3 +-
 .../docs/operate/configuration-reference.md        |   8 +-
 docs/src/content/docs/reference/api-contract.md    |  15 +-
 docs/src/content/docs/reference/api.md             |   2 +-
 docs/src/content/docs/reference/api/events.md      |  13 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |  15 +-
 e2e/helpers/seed.ts                                |  15 +
 e2e/tests/editor-chat.spec.ts                      |  68 ++-
 gflow-prd-v1.md                                    |   8 +-
 internal/ai/openai.go                              |  77 +++-
 internal/ai/openai_test.go                         |  66 +++
 internal/api/handlers/executions.go                |  46 +-
 internal/api/handlers/executions_events_test.go    |  64 +++
 internal/config/config.go                          |  10 +-
 internal/config/config_test.go                     |  22 +
 internal/engine/approval.go                        |   2 +-
 internal/engine/checkpoint.go                      |  14 +-
 internal/engine/runner.go                          |  32 +-
 internal/engine/wait_service_test.go               | 492 ++++++++++++++++++++-
 nodes/ai.go                                        |  75 ++--
 nodes/ai_test.go                                   |  59 +++
 scripts/generate-api-reference.mjs                 |  28 +-
 sdk/src/generated/models.ts                        | 250 +++++++++++
 sidecar/runner_test.go                             |  15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   2 +-
 web/messages/en/editor.json                        |  20 +-
 web/messages/id/editor.json                        |  20 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |  24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |  24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |  24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |  24 +
 .../api/generated/models/aIModelStartedEvent.ts    |  24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |  24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |  24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |  24 +
 web/src/lib/api/generated/models/index.ts          |  10 +
 web/src/lib/api/generated/models/otherEvent.ts     |  24 +
 .../models/streamExecutionEvents200Item.ts         |  90 ++++
 .../api/generated/models/webhookResponseEvent.ts   |  24 +
 .../workflow-editor/canvas-chat-panel.svelte       | 381 +++++++++++++---
 .../workflow-editor/chat-markdown.svelte           |  38 ++
 .../workflow-editor/workflow-editor.svelte         |  54 ++-
 web/src/lib/workflow-editor/chat-markdown.test.ts  | 110 +++++
 web/src/lib/workflow-editor/chat-markdown.ts       | 211 +++++++++
 web/src/lib/workflow-editor/chat-stream.test.ts    |  70 +++
 web/src/lib/workflow-editor/chat-stream.ts         | 112 +++++
 web/src/lib/workflow-editor/chat.test.ts           |  54 ++-
 web/src/lib/workflow-editor/chat.ts                |  78 +++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |  44 +-
 .../lib/workflow-editor/execution-watch.test.ts    |  58 +++
 web/src/lib/workflow-editor/execution-watch.ts     |  56 +++
 web/src/lib/workflow-editor/validation.ts          |   5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  36 +-
 255 files changed, 15347 insertions(+), 553 deletions(-)
```
