---
id: FEAT-7q13t6
title: 'JS Code runtime P1: internal/jsrun core — goja engine seam, limits, interrupts, watchdog, error mapping'
status: done
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-rkj8ry
parent: EPIC-tjnr1z
phase: p1
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T05:34:44Z"
---

# Description

The engine core: a fresh goja VM per node execution, compiled programs cached by source hash, a user-time-only deadline, a layered memory policy and user-line error mapping.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 1* section. Read it before starting.

# Acceptance Criteria
- [ ] `github.com/dop251/goja` pinned. Only `engine*.go` imports the runtime; `analyze*.go` may import the syntax-only packages (EPIC amendment 2)
- [ ] `Runner.Run` with `Limits` and the named errors `ErrTimeLimit`, `ErrMemoryLimit`, `ErrOutputLimit`, `ErrInputLimit`, `ErrHostCallLimit`, `ErrCallDepth`, `ErrInvalidReturn`, plus `*ScriptError`
- [ ] `while(true)`, a loop in a promise job, and ReDoS all stop within the deadline + 50 ms (ReDoS: + `regexp2.DefaultMatchTimeout`, which is at least the ceiling, so a timeout never reads as "no match"). VMs are never reused (EPIC amendment 15)
- [ ] The heap watchdog stops a runaway allocation with `ErrMemoryLimit`, and the process survives
- [ ] The time limit covers the user's program only: a 50 ms limit with a preloaded lodash body passes under `-race` (the Luxon version is in P3)
- [ ] Errors map to `[line N]` / `[line N, for item I]` in user coordinates
- [ ] A source-map comment never reads the filesystem, and a body that closes its wrapper is refused (EPIC amendments 3 and 6)
- [ ] The AST analyser (`internal/jsrun/analyze.go`) refuses unsupported constructs by name (EPIC amendment 7)
- [ ] The flat `code.javascript_*` config keys (EPIC amendment 1) are generated into config.example.yaml and the docs

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 1*.

# Notes

**2026-09-23: implemented.** What the build found beyond the epic's amendments:

- **goja bug: destructuring parameters and direct `eval`.** A direct `eval()` in a function nested inside one with a destructuring parameter list panics inside goja ("index out of range"). The wrapper therefore takes the roots as plain positional parameters (`wrapperVersion` jsrun-2).
- **Every VM entry is guarded.** goja re-panics anything it does not recognise as a JavaScript error, and without a guard that crashed the test process. `guard` in `engine.go` turns such a panic into `ErrEngineFault`: one run fails and the server survives.
- **How the regex timeout is set.** `coverTimeLimit` only ever raises `regexp2.DefaultMatchTimeout`, to `limit + limit/10 + 100ms`. It runs at `init`, for the default 10 s, and again in every `NewRunner`. The current timeout is part of the program cache key and of the library cache key, because goja compiles a regex literal when it compiles the program. A test that lowers it must build its runner first, since `NewRunner` raises it again.
- **Watchdog.** When the heap passes the ceiling, it stops every script that is running, because goja cannot attribute memory to a VM. It then waits for those scripts to be released, collects once, and only then samples again. That way the garbage of a script it just stopped cannot kill the next one.
- **Per-item roots** are read from the parsed input before any user code runs. Between items, nothing touches an object the script can reach, so no accessor can run off the clock.
- **Benchmarks** (M4, no race; each includes a fresh VM):

  | Mode | 10 items | 1000 items |
  |---|---|---|
  | All items | 0.10 ms | 4.0 ms |
  | Each item | 0.09 ms | 7.5 ms |
- **Binary size.** Nothing links jsrun until P4 wires the node, so the size delta is measured at P4.

# Related Files

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (3):
  - `50ebb709` — FEAT-7q13t6: internal/jsrun runs Code-node JavaScript on goja, with the time limit on the user's program only
  - `653f138c` — FEAT-rkj8ry: the JavaScript runtime gets its licence record, its vendored libraries and its import boundary
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          | Bin 0 -> 119127 bytes
 .gitignore                                         |   4 +
 .pine/memory/code-node.md                          |  27 +-
 .pine/memory/licensing.md                          |  11 +-
 .pine/memory/n8n-reference.md                      |   4 +-
 .pine/roadmap.md                                   |   8 +-
 .pine/tickets/BUG-0xv7bg.md                        |  54 ++
 .pine/tickets/BUG-15st2k.md                        | 162 ++++++
 .pine/tickets/BUG-2eryxn.md                        |  49 ++
 .pine/tickets/BUG-2mes2k.md                        |  54 ++
 .pine/tickets/BUG-2n4rfz.md                        |  51 ++
 .pine/tickets/BUG-2z8geh.md                        |  52 ++
 .pine/tickets/BUG-3k12ky.md                        | 163 ++++++
 .pine/tickets/BUG-3qxx0j.md                        |  59 ++
 .pine/tickets/BUG-56qqgx.md                        |  52 ++
 .pine/tickets/BUG-5bgx5c.md                        |  59 ++
 .pine/tickets/BUG-605n21.md                        | 131 +++++
 .pine/tickets/BUG-66fhea.md                        |  97 ++++
 .pine/tickets/BUG-6d6wbg.md                        |  98 ++++
 .pine/tickets/BUG-6gkd12.md                        | 107 ++++
 .pine/tickets/BUG-719gaz.md                        |  88 +++
 .pine/tickets/BUG-9dw5me.md                        |  53 ++
 .pine/tickets/BUG-9pmv8y.md                        |  61 +++
 .pine/tickets/BUG-b3p8va.md                        |  61 +++
 .pine/tickets/BUG-b4cb1c.md                        | 119 +++++
 .pine/tickets/BUG-b8bwhw.md                        |  55 ++
 .pine/tickets/BUG-bcahaj.md                        | 100 ++++
 .pine/tickets/BUG-bw2zc1.md                        |  55 ++
 .pine/tickets/BUG-dstsg9.md                        |  54 ++
 .pine/tickets/BUG-e7dwpk.md                        |  51 ++
 .pine/tickets/BUG-ecbq28.md                        | 111 ++++
 .pine/tickets/BUG-epy2se.md                        | 122 +++++
 .pine/tickets/BUG-g7ffj1.md                        |  50 ++
 .pine/tickets/BUG-hmp85t.md                        |  99 ++++
 .pine/tickets/BUG-j7qrp2.md                        |  52 ++
 .pine/tickets/BUG-mzk0xn.md                        |  35 ++
 .pine/tickets/BUG-n6p7qy.md                        | 101 ++++
 .pine/tickets/BUG-n9a6bz.md                        |  54 ++
 .pine/tickets/BUG-namghh.md                        |  48 ++
 .pine/tickets/BUG-nbymq4.md                        |  52 ++
 .pine/tickets/BUG-ngt25j.md                        |  52 ++
 .pine/tickets/BUG-nn74ph.md                        |  51 ++
 .pine/tickets/BUG-nzy3pa.md                        | 186 +++++++
 .pine/tickets/BUG-p334yw.md                        |  53 ++
 .pine/tickets/BUG-p3j233.md                        |  53 ++
 .pine/tickets/BUG-phv0r9.md                        |  56 ++
 .pine/tickets/BUG-ppvyzr.md                        |  58 ++
 .pine/tickets/BUG-pzkpfr.md                        |  53 ++
 .pine/tickets/BUG-q6b75c.md                        |  52 ++
 .pine/tickets/BUG-r1m83f.md                        |  52 ++
 .pine/tickets/BUG-rbask0.md                        | 140 +++++
 .pine/tickets/BUG-rh7mpa.md                        |  52 ++
 .pine/tickets/BUG-rs0xq1.md                        |  46 ++
 .pine/tickets/BUG-rytwy7.md                        |  55 ++
 .pine/tickets/BUG-sgrxhh.md                        |  53 ++
 .pine/tickets/BUG-t12ffz.md                        |  57 ++
 .pine/tickets/BUG-t3p92b.md                        |  94 ++++
 .pine/tickets/BUG-txafja.md                        |  55 ++
 .pine/tickets/BUG-v8ksv8.md                        |  49 ++
 .pine/tickets/BUG-vsmnby.md                        |  36 ++
 .pine/tickets/BUG-x28fsx.md                        | 142 +++++
 .pine/tickets/BUG-x6gyc1.md                        |  54 ++
 .pine/tickets/BUG-xam6t8.md                        | 179 +++++++
 .pine/tickets/BUG-y38bss.md                        |  54 ++
 .pine/tickets/BUG-ywbvfa.md                        |  36 ++
 .pine/tickets/BUG-z0s4zg.md                        | 100 ++++
 .pine/tickets/BUG-zf4pnj.md                        |  55 ++
 .pine/tickets/EPIC-3en6xr.md                       |  88 +++
 .pine/tickets/EPIC-62zt4j.md                       | 110 ++++
 .pine/tickets/EPIC-7c3ry9.md                       |  44 ++
 .pine/tickets/EPIC-8rbys7.md                       | 192 +++++++
 .pine/tickets/EPIC-m42s3g.md                       |   2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 536 +++++++++++++++++++
 .pine/tickets/FEAT-02cj1g.md                       | 102 ++++
 .pine/tickets/FEAT-02zdcq.md                       | 169 ++++++
 .pine/tickets/FEAT-0hdfzd.md                       | 106 ++++
 .pine/tickets/FEAT-0xsc1s.md                       |  35 ++
 .pine/tickets/FEAT-1ge0xc.md                       |  31 ++
 .pine/tickets/FEAT-1mxtsn.md                       | 104 ++++
 .pine/tickets/FEAT-274c4p.md                       |  68 +++
 .pine/tickets/FEAT-27g2za.md                       |  33 ++
 .pine/tickets/FEAT-2kx0hx.md                       | 260 +++++++++
 .pine/tickets/FEAT-2m24nh.md                       |  53 ++
 .pine/tickets/FEAT-2m4yvz.md                       | 101 ++++
 .pine/tickets/FEAT-38je8w.md                       |  35 ++
 .pine/tickets/FEAT-39ttf6.md                       |  32 ++
 .pine/tickets/FEAT-3t112f.md                       |  53 ++
 .pine/tickets/FEAT-3ykb4v.md                       |  37 ++
 .pine/tickets/FEAT-4bjfny.md                       | 100 ++++
 .pine/tickets/FEAT-4bvcrb.md                       |  38 ++
 .pine/tickets/FEAT-4e376e.md                       |  56 ++
 .pine/tickets/FEAT-4jhtny.md                       |  30 ++
 .pine/tickets/FEAT-4pz9fn.md                       |  37 ++
 .pine/tickets/FEAT-53pa9a.md                       |  52 ++
 .pine/tickets/FEAT-5fx926.md                       |  57 ++
 .pine/tickets/FEAT-5g42rz.md                       |  30 ++
 .pine/tickets/FEAT-5kv1jq.md                       |   8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |  99 ++++
 .pine/tickets/FEAT-6m295t.md                       |  38 ++
 .pine/tickets/FEAT-6qzza1.md                       |  58 ++
 .pine/tickets/FEAT-6r663e.md                       |  32 ++
 .pine/tickets/FEAT-70j6dn.md                       |  55 ++
 .pine/tickets/FEAT-7cg0cd.md                       |   6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  57 ++
 .pine/tickets/FEAT-7t0xks.md                       |  31 ++
 .pine/tickets/FEAT-8752vx.md                       |  54 ++
 .pine/tickets/FEAT-8zgwp6.md                       |  32 ++
 .pine/tickets/FEAT-9ep5pw.md                       |  31 ++
 .pine/tickets/FEAT-a3dwj2.md                       |  52 ++
 .pine/tickets/FEAT-afkx3k.md                       |  37 ++
 .pine/tickets/FEAT-bfrkyk.md                       |  54 ++
 .pine/tickets/FEAT-c81kp3.md                       |  59 ++
 .pine/tickets/FEAT-cgm1y3.md                       |   2 +-
 .pine/tickets/FEAT-csqgg5.md                       |   6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |  59 ++
 .pine/tickets/FEAT-edzr73.md                       | 121 +++++
 .pine/tickets/FEAT-egm8bf.md                       |  37 ++
 .pine/tickets/FEAT-eqzpzq.md                       | 136 +++++
 .pine/tickets/FEAT-ez6xtm.md                       |  55 ++
 .pine/tickets/FEAT-f045nj.md                       | 131 +++++
 .pine/tickets/FEAT-f3hx3a.md                       |  37 ++
 .pine/tickets/FEAT-fpqg78.md                       |  52 ++
 .pine/tickets/FEAT-fqmh01.md                       |  97 ++++
 .pine/tickets/FEAT-fs3pjr.md                       | 205 +++++++
 .pine/tickets/FEAT-gzd32h.md                       |  31 ++
 .pine/tickets/FEAT-hxztwz.md                       |  37 ++
 .pine/tickets/FEAT-je4f4t.md                       |   4 +-
 .pine/tickets/FEAT-jwhdsy.md                       |   2 +-
 .pine/tickets/FEAT-kcdrcy.md                       | 130 +++++
 .pine/tickets/FEAT-kfmq1z.md                       |  53 ++
 .pine/tickets/FEAT-kpn0m3.md                       |  37 ++
 .pine/tickets/FEAT-ktasef.md                       | 103 ++++
 .pine/tickets/FEAT-ky75b5.md                       |  52 ++
 .pine/tickets/FEAT-m1fdn4.md                       |  56 ++
 .pine/tickets/FEAT-m7aw75.md                       |  54 ++
 .pine/tickets/FEAT-mammrz.md                       |  35 ++
 .pine/tickets/FEAT-mccadj.md                       |  38 ++
 .pine/tickets/FEAT-mh4e8g.md                       |  32 ++
 .pine/tickets/FEAT-mj2nek.md                       |  98 ++++
 .pine/tickets/FEAT-mngmn1.md                       |  32 ++
 .pine/tickets/FEAT-mq412g.md                       |  58 ++
 .pine/tickets/FEAT-mxmjt7.md                       | 129 +++++
 .pine/tickets/FEAT-n010f0.md                       |  33 ++
 .pine/tickets/FEAT-n12211.md                       |  34 ++
 .pine/tickets/FEAT-nch9dg.md                       |   6 +-
 .pine/tickets/FEAT-npc3ge.md                       |  37 ++
 .pine/tickets/FEAT-nq1vsx.md                       |  53 ++
 .pine/tickets/FEAT-p01rcw.md                       |  98 ++++
 .pine/tickets/FEAT-p75n7j.md                       |  38 ++
 .pine/tickets/FEAT-pfwjzk.md                       |  30 ++
 .pine/tickets/FEAT-ppnetz.md                       | 141 +++++
 .pine/tickets/FEAT-pqnxx4.md                       |  37 ++
 .pine/tickets/FEAT-prw1hw.md                       |  56 ++
 .pine/tickets/FEAT-pt6ge9.md                       |  34 ++
 .pine/tickets/FEAT-pxcbqj.md                       |  39 ++
 .pine/tickets/FEAT-q81bq4.md                       |   2 +-
 .pine/tickets/FEAT-qf0hsa.md                       |  53 ++
 .pine/tickets/FEAT-r267jj.md                       |  35 ++
 .pine/tickets/FEAT-r8ph93.md                       |  38 ++
 .pine/tickets/FEAT-rdfjh1.md                       |  32 ++
 .pine/tickets/FEAT-re138f.md                       |  54 ++
 .pine/tickets/FEAT-rkj8ry.md                       | 319 +++++++++++
 .pine/tickets/FEAT-s3sfx5.md                       |  31 ++
 .pine/tickets/FEAT-s99vdp.md                       | 155 ++++++
 .pine/tickets/FEAT-sc3qrq.md                       |  54 ++
 .pine/tickets/FEAT-sz4ddp.md                       |  57 ++
 .pine/tickets/FEAT-t26rt7.md                       |   2 +-
 .pine/tickets/FEAT-t38djq.md                       |  56 ++
 .pine/tickets/FEAT-t58m89.md                       |  32 ++
 .pine/tickets/FEAT-t672pv.md                       |  57 ++
 .pine/tickets/FEAT-tjcr13.md                       |  52 ++
 .pine/tickets/FEAT-v2nenc.md                       |  58 ++
 .pine/tickets/FEAT-vjjs8t.md                       |  36 ++
 .pine/tickets/FEAT-vntngh.md                       |  64 +++
 .pine/tickets/FEAT-vvwpjw.md                       |   2 +-
 .pine/tickets/FEAT-w7n7x6.md                       | 131 +++++
 .pine/tickets/FEAT-w9kqeg.md                       |  10 +-
 .pine/tickets/FEAT-wcr6en.md                       |  52 ++
 .pine/tickets/FEAT-wzfz3d.md                       |  59 ++
 .pine/tickets/FEAT-x9gq0s.md                       |  37 ++
 .pine/tickets/FEAT-xj5tv6.md                       |  38 ++
 .pine/tickets/FEAT-xr75b9.md                       |  58 ++
 .pine/tickets/FEAT-xzdn35.md                       |  56 ++
 .pine/tickets/FEAT-ybm2pd.md                       |   2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |  32 ++
 .pine/tickets/FEAT-ys734v.md                       |  36 ++
 .pine/tickets/FEAT-yxhgeh.md                       |  80 +++
 .pine/tickets/FEAT-yyjfjq.md                       |   2 +-
 .pine/tickets/FEAT-z90r5a.md                       |  32 ++
 .pine/tickets/FEAT-zhdxc4.md                       |  38 ++
 .pine/tickets/FEAT-zjrw76.md                       |  37 ++
 .pine/tickets/FEAT-zm3wh2.md                       |  99 ++++
 .pine/tickets/FEAT-zn5rqy.md                       | 103 ++++
 .pine/tickets/FEAT-zwpvbf.md                       |  60 +++
 CHANGELOG.md                                       |  25 +
 CONTRIBUTING.md                                    |  22 +
 README.md                                          | 450 +++++-----------
 config.example.yaml                                |  45 +-
 docs/src/content/docs/concepts/architecture.md     |  84 +++
 docs/src/content/docs/concepts/execution-model.md  |  17 +-
 docs/src/content/docs/concepts/node-registry.md    |   5 +-
 .../src/content/docs/concepts/safety-boundaries.md |   2 +-
 docs/src/content/docs/concepts/webhooks.md         |  33 ++
 docs/src/content/docs/guides/community-nodes.md    |   6 +-
 .../content/docs/operate/acceptance-capstone.md    |   3 +-
 .../docs/operate/configuration-reference.md        |  96 +++-
 docs/src/content/docs/reference/api-contract.md    |  16 +-
 docs/src/content/docs/reference/api.md             |   2 +-
 docs/src/content/docs/reference/api/events.md      |  14 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |  15 +-
 e2e/helpers/seed.ts                                |  15 +
 e2e/tests/editor-chat.spec.ts                      |  68 ++-
 gflow-prd-v1.md                                    |   8 +-
 go.mod                                             |   4 +
 go.sum                                             |   8 +
 internal/ai/openai.go                              |  77 ++-
 internal/ai/openai_test.go                         |  66 +++
 internal/api/handlers/execution_console_test.go    |  62 +++
 internal/api/handlers/executions.go                |  50 +-
 internal/api/handlers/executions_events_test.go    |  65 +++
 internal/api/handlers/workflows.go                 |   7 +-
 internal/config/config.go                          | 100 +++-
 internal/config/config_test.go                     | 105 ++++
 internal/database/workflow_actor_migration_test.go |  27 +-
 internal/engine/approval.go                        |   2 +-
 internal/engine/authenticate.go                    |   1 +
 internal/engine/console_test.go                    | 397 ++++++++++++++
 internal/engine/runindex_test.go                   |  70 +++
 internal/engine/runner.go                          | 116 +++-
 internal/engine/service.go                         |   4 +-
 internal/engine/wait_service.go                    |   2 +-
 internal/execution/records.go                      |   7 +
 internal/guardrails/jsruntime_boundary_test.go     | 309 +++++++++++
 internal/jsrun/analyze.go                          | 476 +++++++++++++++++
 internal/jsrun/analyze_test.go                     | 170 ++++++
 internal/jsrun/clock.go                            |  57 ++
 internal/jsrun/console.go                          |  61 +++
 internal/jsrun/doc.go                              |  72 +++
 internal/jsrun/engine.go                           | 555 +++++++++++++++++++
 internal/jsrun/errors.go                           | 159 ++++++
 internal/jsrun/export_test.go                      |  79 +++
 internal/jsrun/guards_test.go                      | 118 ++++
 internal/jsrun/items.go                            | 159 ++++++
 internal/jsrun/js/runtime.js                       | 595 +++++++++++++++++++++
 internal/jsrun/jsrun.go                            | 203 +++++++
 internal/jsrun/jsrun_test.go                       | 525 ++++++++++++++++++
 internal/jsrun/libraries.go                        |  65 +++
 internal/jsrun/norace_test.go                      |   7 +
 internal/jsrun/programs.go                         | 122 +++++
 internal/jsrun/race_test.go                        |   8 +
 internal/jsrun/roots.go                            | 100 ++++
 internal/jsrun/roots_test.go                       | 415 ++++++++++++++
 internal/jsrun/run.go                              | 203 +++++++
 internal/jsrun/watchdog.go                         | 119 +++++
 internal/jsrun/wrapper.go                          |  81 +++
 internal/repository/executions.go                  |  11 +
 internal/repository/models.go                      |   6 +-
 internal/repository/node_run_console_test.go       | 117 ++++
 .../postgres/000021_node_run_console.down.sql      |   8 +
 migrations/postgres/000021_node_run_console.up.sql |  25 +
 migrations/sqlite/000021_node_run_console.down.sql |   8 +
 migrations/sqlite/000021_node_run_console.up.sql   |  21 +
 nodes/ai.go                                        |  75 +--
 nodes/ai_test.go                                   |  59 ++
 nodes/jscode_roots.go                              |  45 ++
 nodes/jscode_roots_test.go                         |  99 ++++
 scripts/generate-api-reference.mjs                 |  30 +-
 sdk/src/generated/models.ts                        | 277 ++++++++++
 sidecar/doc.go                                     |   2 +-
 sidecar/runner_test.go                             |  15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   2 +-
 third_party/lodash/LICENSE                         |  47 ++
 third_party/lodash/PROVENANCE.md                   |  32 ++
 third_party/lodash/embed.go                        |  15 +
 third_party/lodash/lodash.min.js                   | 136 +++++
 third_party/luxon/LICENSE                          |   7 +
 third_party/luxon/PROVENANCE.md                    |  34 ++
 third_party/luxon/embed.go                         |  15 +
 third_party/luxon/luxon.min.js                     |   1 +
 third_party/waha/PROVENANCE.md                     |   5 +-
 web/messages/en/editor.json                        |  20 +-
 web/messages/en/executions.json                    |   2 +
 web/messages/id/editor.json                        |  20 +-
 web/messages/id/executions.json                    |   2 +
 .../api/generated/models/aIAgentCompletedEvent.ts  |  24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |  24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |  24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |  24 +
 .../api/generated/models/aIModelStartedEvent.ts    |  24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |  24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |  24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |  24 +
 .../lib/api/generated/models/codeConsoleEvent.ts   |  24 +
 .../generated/models/executionNodeRunResource.ts   |   2 +
 web/src/lib/api/generated/models/index.ts          |  11 +
 web/src/lib/api/generated/models/otherEvent.ts     |  24 +
 .../models/streamExecutionEvents200Item.ts         |  99 ++++
 .../api/generated/models/webhookResponseEvent.ts   |  24 +
 .../workflow-editor/canvas-chat-panel.svelte       | 381 +++++++++++--
 .../workflow-editor/chat-markdown.svelte           |  38 ++
 .../workflow-editor/workflow-editor.svelte         |  54 +-
 web/src/lib/workflow-editor/chat-markdown.test.ts  | 110 ++++
 web/src/lib/workflow-editor/chat-markdown.ts       | 211 ++++++++
 web/src/lib/workflow-editor/chat-stream.test.ts    |  70 +++
 web/src/lib/workflow-editor/chat-stream.ts         | 112 ++++
 web/src/lib/workflow-editor/chat.test.ts           |  54 +-
 web/src/lib/workflow-editor/chat.ts                |  78 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |  45 +-
 .../lib/workflow-editor/execution-watch.test.ts    |  58 ++
 web/src/lib/workflow-editor/execution-watch.ts     |  56 ++
 web/src/lib/workflow-editor/validation.ts          |   5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  36 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  55 ++
 313 files changed, 21628 insertions(+), 587 deletions(-)
```
