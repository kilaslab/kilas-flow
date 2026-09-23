---
id: FEAT-yxhgeh
title: 'JS Code runtime P2: n8n Code-node globals shim (clean-room), modes, return normalisation, console capture'
status: done
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-7q13t6
parent: EPIC-tjnr1z
phase: p2
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T07:51:41Z"
---

# Description

The globals are built from `request.ExpressionContext`, so a Code node sees exactly what an expression sees.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 2* section. Read it before starting.

# Acceptance Criteria
- [ ] The all-items and per-item modes behave as documented, and user code may redeclare any root (`const items = $input.all()`)
- [ ] `$input`, `items`, `$json`, `$('X')` (`all`/`first`/`last`/`item`/`itemMatching`/`params`/`isExecuted`), `$node`, `$workflow`, `$execution`, `$env`, `$vars`, `$now`, `$today` and `$jmespath` return what an expression sees
- [ ] Return normalisation wraps plain objects; invalid returns are named errors; explicit `pairedItem` wins
- [ ] `console.*` is captured into `NodeRun.Console`, persisted and emitted live, and capped at `MaxConsoleBytes`
- [ ] Tests are written in KilasFlow's own words; no n8n doc snippets are copied verbatim

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 2*.

# Notes

**2026-09-23: implemented.**

**The roots.**
- The roots live in `internal/jsrun/js/runtime.js` (`install`), which is KilasFlow-authored and trusted. The runner reaches them only through the object that script returns.
- The per-call arguments are built by that trusted `run(body, index)` function inside the charged window, so no user accessor can run off the clock between items.
- `$('X')` and `$node[...]` fetch a node's items from the host the first time the code names that node (`Roots.Node`). A body that never reads another node never pays to serialise it.
- `.item` and `.itemMatching(i)` ask `Roots.Pair`, which is `engine.PairedIndex`. That function was factored out of `pairNodeItem`, so code and expressions pair items by one algorithm (`TestTheCodeRootsMatchWhatAnExpressionSees`).
- In all-items mode, `.item` and `$input.item` resolve against item 0.

**Return handling.**
- Lineage precedence: explicit `pairedItem`, then the identity of the returned object (the item, or its `json`), then per-item position. Anything else is left to the runner's positional inference.
- Several sources are recorded as lost.
- A paired output inherits the input item's origin, which is what the runner does itself for a one-to-one node.
- Binary crosses as metadata only, is dropped unless returned, and a returned id must be one of the input's files.

**Unsupported roots and helpers.**
- `$jmespath`, `$prevNode`, `$secrets`, `$evaluateExpression`, `$getWorkflowStaticData`, `$execution.customData`, `.all(branch, run)` and `this.helpers.*` fail at run time in the `Refusal` sentence.
- Mode-inapplicable roots (`$json` and `$itemIndex` in all-items mode, `items` per item) are getters that throw with their name.

**RegExp guard.** A pattern built at run time is checked by a `RegExp` stand-in. It is a plain function sharing `RegExp.prototype`, because goja cannot use a Proxy on the right of `instanceof`.

**Console.**
- Node-style formatting, placeholders included, and a bounded inspect.
- It is kept up to `MaxConsoleBytes` and kept on failure too.
- The engine captures `code.console` events into `NodeRun.Console` the way it captures `webhook.response`.
- It is persisted through migration `000021_node_run_console`, exposed as `console` on the node-run resource and as a typed `code.console` SSE event, and shown on the execution detail page.

**`$runIndex`.** It was always 0 in expressions. `Request.RunIndex` is now set per invocation.

**P1 security review fixes in this phase.** The details are in `.pine/memory/code-node.md`, "goja's hazards".
- Exponential constant folding is refused at analysis: constant-expression depth ≤ 16.
- Parsing and compiling are bounded to GOMAXPROCS at once.
- Asynchronous host calls recover their own panics.
- Built-ins that allocate or loop as far as a number tells them are capped per call (`boundAllocations`).
- The array methods are JS wrappers, so native recursion through nested arrays counts against the call-depth limit (it had run 80 s past a 10 s limit).
- Libraries load after the guards.
- Cost: about 0.3 ms more setup per VM, which is not charged.

**P1 review fixes in this phase.**
- Bodies are limited before goja parses them: 128 KiB of source and 1000 arrow functions, checked on the raw text, and 1000 levels of AST nesting, checked before compile. A Go stack overflow in goja's recursive parser or compiler is fatal to the server (400,000 nested parentheses crashed the process), and nested blocks and arrows compile or parse in quadratic time.
- The output-size early stop now counts a lower bound, so output that fits is never refused.

# Related Files

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (5):
  - `71b7775b` — merge: FEAT-yxhgeh runtime.js review fixes (allocation bounds, stringify, stacks, formatting)
  - `33933f06` — FEAT-yxhgeh: review fixes: bounded built-ins read what they check once, the unchecked constructors are out of reach, and the console prints as Node's does
  - `b527da25` — FEAT-pxcbqj: the Code (JavaScript) node runs imported and new JS, and the importer keeps it runnable
  - `0152e837` — FEAT-yxhgeh: Code-node JavaScript sees n8n's globals, keeps its lineage, and its console output is kept with the run
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          |  Bin 0 -> 119127 bytes
 .gitignore                                         |    4 +
 .pine/memory/code-node.md                          |   27 +-
 .pine/memory/licensing.md                          |   11 +-
 .pine/memory/n8n-reference.md                      |    4 +-
 .pine/roadmap.md                                   |    8 +-
 .pine/tickets/BUG-0xv7bg.md                        |   54 +
 .pine/tickets/BUG-14gp8r.md                        |   68 +
 .pine/tickets/BUG-15st2k.md                        |  162 +
 .pine/tickets/BUG-2eryxn.md                        |   49 +
 .pine/tickets/BUG-2mes2k.md                        |   54 +
 .pine/tickets/BUG-2n4rfz.md                        |   51 +
 .pine/tickets/BUG-2z8geh.md                        |   52 +
 .pine/tickets/BUG-3k12ky.md                        |  163 +
 .pine/tickets/BUG-3qxx0j.md                        |   59 +
 .pine/tickets/BUG-548bk9.md                        |   46 +
 .pine/tickets/BUG-56qqgx.md                        |   52 +
 .pine/tickets/BUG-5bgx5c.md                        |   59 +
 .pine/tickets/BUG-605n21.md                        |  131 +
 .pine/tickets/BUG-66fhea.md                        |   97 +
 .pine/tickets/BUG-6d6wbg.md                        |   98 +
 .pine/tickets/BUG-6gkd12.md                        |  107 +
 .pine/tickets/BUG-719gaz.md                        |   88 +
 .pine/tickets/BUG-9dw5me.md                        |   53 +
 .pine/tickets/BUG-9pmv8y.md                        |   61 +
 .pine/tickets/BUG-b3p8va.md                        |   61 +
 .pine/tickets/BUG-b4cb1c.md                        |  119 +
 .pine/tickets/BUG-b8bwhw.md                        |   55 +
 .pine/tickets/BUG-bcahaj.md                        |  100 +
 .pine/tickets/BUG-bw2zc1.md                        |   55 +
 .pine/tickets/BUG-dstsg9.md                        |   54 +
 .pine/tickets/BUG-e7dwpk.md                        |   51 +
 .pine/tickets/BUG-ecbq28.md                        |  111 +
 .pine/tickets/BUG-epy2se.md                        |  122 +
 .pine/tickets/BUG-fthahg.md                        |   58 +
 .pine/tickets/BUG-g7ffj1.md                        |   50 +
 .pine/tickets/BUG-hmp85t.md                        |   99 +
 .pine/tickets/BUG-j7qrp2.md                        |   52 +
 .pine/tickets/BUG-mzk0xn.md                        |   35 +
 .pine/tickets/BUG-n6p7qy.md                        |  101 +
 .pine/tickets/BUG-n9a6bz.md                        |   54 +
 .pine/tickets/BUG-namghh.md                        |   48 +
 .pine/tickets/BUG-nbymq4.md                        |   52 +
 .pine/tickets/BUG-ngt25j.md                        |   52 +
 .pine/tickets/BUG-nn74ph.md                        |   51 +
 .pine/tickets/BUG-nzy3pa.md                        |  186 ++
 .pine/tickets/BUG-p334yw.md                        |   53 +
 .pine/tickets/BUG-p3j233.md                        |   53 +
 .pine/tickets/BUG-phv0r9.md                        |   56 +
 .pine/tickets/BUG-ppvyzr.md                        |   58 +
 .pine/tickets/BUG-pzkpfr.md                        |   53 +
 .pine/tickets/BUG-q6b75c.md                        |   52 +
 .pine/tickets/BUG-r1m83f.md                        |   52 +
 .pine/tickets/BUG-rbask0.md                        |  140 +
 .pine/tickets/BUG-rh7mpa.md                        |   52 +
 .pine/tickets/BUG-rs0xq1.md                        |   46 +
 .pine/tickets/BUG-rytwy7.md                        |   55 +
 .pine/tickets/BUG-sgrxhh.md                        |   53 +
 .pine/tickets/BUG-t12ffz.md                        |   57 +
 .pine/tickets/BUG-t3p92b.md                        |   94 +
 .pine/tickets/BUG-txafja.md                        |   55 +
 .pine/tickets/BUG-v8ksv8.md                        |   49 +
 .pine/tickets/BUG-vsmnby.md                        |   36 +
 .pine/tickets/BUG-x28fsx.md                        |  142 +
 .pine/tickets/BUG-x6gyc1.md                        |   54 +
 .pine/tickets/BUG-xam6t8.md                        |  179 ++
 .pine/tickets/BUG-y38bss.md                        |   54 +
 .pine/tickets/BUG-ywbvfa.md                        |   36 +
 .pine/tickets/BUG-z0s4zg.md                        |  100 +
 .pine/tickets/BUG-zf4pnj.md                        |   55 +
 .pine/tickets/EPIC-3en6xr.md                       |   88 +
 .pine/tickets/EPIC-62zt4j.md                       |  110 +
 .pine/tickets/EPIC-7c3ry9.md                       |   44 +
 .pine/tickets/EPIC-8rbys7.md                       |  192 ++
 .pine/tickets/EPIC-m42s3g.md                       |    2 +-
 .pine/tickets/EPIC-tjnr1z.md                       |  536 ++++
 .pine/tickets/FEAT-02cj1g.md                       |  102 +
 .pine/tickets/FEAT-02zdcq.md                       |  169 ++
 .pine/tickets/FEAT-0hdfzd.md                       |  106 +
 .pine/tickets/FEAT-0xsc1s.md                       |   35 +
 .pine/tickets/FEAT-1ge0xc.md                       |   31 +
 .pine/tickets/FEAT-1mxtsn.md                       |  104 +
 .pine/tickets/FEAT-21h6xp.md                       |   45 +
 .pine/tickets/FEAT-274c4p.md                       |   68 +
 .pine/tickets/FEAT-27g2za.md                       |   33 +
 .pine/tickets/FEAT-2kx0hx.md                       |  260 ++
 .pine/tickets/FEAT-2m24nh.md                       |   53 +
 .pine/tickets/FEAT-2m4yvz.md                       |  101 +
 .pine/tickets/FEAT-38je8w.md                       |   35 +
 .pine/tickets/FEAT-39ttf6.md                       |   32 +
 .pine/tickets/FEAT-3t112f.md                       |   53 +
 .pine/tickets/FEAT-3ykb4v.md                       |   37 +
 .pine/tickets/FEAT-4bjfny.md                       |  100 +
 .pine/tickets/FEAT-4bvcrb.md                       |   38 +
 .pine/tickets/FEAT-4e376e.md                       |   56 +
 .pine/tickets/FEAT-4jhtny.md                       |   30 +
 .pine/tickets/FEAT-4pz9fn.md                       |   37 +
 .pine/tickets/FEAT-53pa9a.md                       |   52 +
 .pine/tickets/FEAT-5fx926.md                       |   57 +
 .pine/tickets/FEAT-5g42rz.md                       |   30 +
 .pine/tickets/FEAT-5kv1jq.md                       |    8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |   99 +
 .pine/tickets/FEAT-6m295t.md                       |   38 +
 .pine/tickets/FEAT-6qzza1.md                       |   58 +
 .pine/tickets/FEAT-6r663e.md                       |   32 +
 .pine/tickets/FEAT-70j6dn.md                       |   55 +
 .pine/tickets/FEAT-7cg0cd.md                       |    6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  385 +++
 .pine/tickets/FEAT-7t0xks.md                       |   31 +
 .pine/tickets/FEAT-8752vx.md                       |   54 +
 .pine/tickets/FEAT-8zgwp6.md                       |   32 +
 .pine/tickets/FEAT-9ep5pw.md                       |   31 +
 .pine/tickets/FEAT-9we7kw.md                       |   38 +
 .pine/tickets/FEAT-a3dwj2.md                       |   52 +
 .pine/tickets/FEAT-afkx3k.md                       |   37 +
 .pine/tickets/FEAT-bfrkyk.md                       |   54 +
 .pine/tickets/FEAT-c81kp3.md                       |   59 +
 .pine/tickets/FEAT-cgm1y3.md                       |    2 +-
 .pine/tickets/FEAT-csqgg5.md                       |    6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |   59 +
 .pine/tickets/FEAT-edzr73.md                       |  121 +
 .pine/tickets/FEAT-egm8bf.md                       |   37 +
 .pine/tickets/FEAT-eqzpzq.md                       |  136 +
 .pine/tickets/FEAT-ez6xtm.md                       |   55 +
 .pine/tickets/FEAT-f045nj.md                       |  131 +
 .pine/tickets/FEAT-f3hx3a.md                       |   37 +
 .pine/tickets/FEAT-fpqg78.md                       |   52 +
 .pine/tickets/FEAT-fqmh01.md                       |   97 +
 .pine/tickets/FEAT-fs3pjr.md                       |  205 ++
 .pine/tickets/FEAT-g6k3y9.md                       |   76 +
 .pine/tickets/FEAT-gzd32h.md                       |   31 +
 .pine/tickets/FEAT-hxztwz.md                       |   37 +
 .pine/tickets/FEAT-je4f4t.md                       |    4 +-
 .pine/tickets/FEAT-jwhdsy.md                       |    2 +-
 .pine/tickets/FEAT-kcdrcy.md                       |  130 +
 .pine/tickets/FEAT-kfmq1z.md                       |   53 +
 .pine/tickets/FEAT-kpn0m3.md                       |   37 +
 .pine/tickets/FEAT-ktasef.md                       |  103 +
 .pine/tickets/FEAT-ky75b5.md                       |   52 +
 .pine/tickets/FEAT-m1fdn4.md                       |   56 +
 .pine/tickets/FEAT-m7aw75.md                       |   54 +
 .pine/tickets/FEAT-mammrz.md                       |   35 +
 .pine/tickets/FEAT-mccadj.md                       |   38 +
 .pine/tickets/FEAT-mh4e8g.md                       |   32 +
 .pine/tickets/FEAT-mj2nek.md                       |   98 +
 .pine/tickets/FEAT-mngmn1.md                       |   32 +
 .pine/tickets/FEAT-mq412g.md                       |   58 +
 .pine/tickets/FEAT-mxmjt7.md                       |  129 +
 .pine/tickets/FEAT-n010f0.md                       |   33 +
 .pine/tickets/FEAT-n12211.md                       |   34 +
 .pine/tickets/FEAT-nch9dg.md                       |    6 +-
 .pine/tickets/FEAT-npc3ge.md                       |   37 +
 .pine/tickets/FEAT-nq1vsx.md                       |   53 +
 .pine/tickets/FEAT-p01rcw.md                       |   98 +
 .pine/tickets/FEAT-p75n7j.md                       |   38 +
 .pine/tickets/FEAT-pfwjzk.md                       |   30 +
 .pine/tickets/FEAT-ppnetz.md                       |  141 +
 .pine/tickets/FEAT-pqnxx4.md                       |   37 +
 .pine/tickets/FEAT-prw1hw.md                       |   56 +
 .pine/tickets/FEAT-pt6ge9.md                       |   34 +
 .pine/tickets/FEAT-pxcbqj.md                       |   58 +
 .pine/tickets/FEAT-q81bq4.md                       |    2 +-
 .pine/tickets/FEAT-qf0hsa.md                       |   53 +
 .pine/tickets/FEAT-r267jj.md                       |   35 +
 .pine/tickets/FEAT-r8ph93.md                       |   38 +
 .pine/tickets/FEAT-rdfjh1.md                       |   32 +
 .pine/tickets/FEAT-re138f.md                       |   54 +
 .pine/tickets/FEAT-rkj8ry.md                       |  319 ++
 .pine/tickets/FEAT-s3sfx5.md                       |   31 +
 .pine/tickets/FEAT-s99vdp.md                       |  155 +
 .pine/tickets/FEAT-sc3qrq.md                       |   54 +
 .pine/tickets/FEAT-sz4ddp.md                       |   57 +
 .pine/tickets/FEAT-t26rt7.md                       |    2 +-
 .pine/tickets/FEAT-t38djq.md                       |   56 +
 .pine/tickets/FEAT-t58m89.md                       |   32 +
 .pine/tickets/FEAT-t672pv.md                       |   57 +
 .pine/tickets/FEAT-tjcr13.md                       |   52 +
 .pine/tickets/FEAT-v2nenc.md                       |   58 +
 .pine/tickets/FEAT-vjjs8t.md                       |   36 +
 .pine/tickets/FEAT-vntngh.md                       |   64 +
 .pine/tickets/FEAT-vvwpjw.md                       |    2 +-
 .pine/tickets/FEAT-w7n7x6.md                       |  131 +
 .pine/tickets/FEAT-w9kqeg.md                       |   10 +-
 .pine/tickets/FEAT-wcr6en.md                       |   52 +
 .pine/tickets/FEAT-wzfz3d.md                       |   59 +
 .pine/tickets/FEAT-x9gq0s.md                       |   37 +
 .pine/tickets/FEAT-xj5tv6.md                       |   38 +
 .pine/tickets/FEAT-xr75b9.md                       |   58 +
 .pine/tickets/FEAT-xzdn35.md                       |   56 +
 .pine/tickets/FEAT-ybm2pd.md                       |    2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |   32 +
 .pine/tickets/FEAT-ys734v.md                       |   36 +
 .pine/tickets/FEAT-yxhgeh.md                       |   80 +
 .pine/tickets/FEAT-yyjfjq.md                       |    2 +-
 .pine/tickets/FEAT-z90r5a.md                       |   32 +
 .pine/tickets/FEAT-zhdxc4.md                       |   38 +
 .pine/tickets/FEAT-zjrw76.md                       |   37 +
 .pine/tickets/FEAT-zm3wh2.md                       |   99 +
 .pine/tickets/FEAT-zn5rqy.md                       |  103 +
 .pine/tickets/FEAT-zwpvbf.md                       |   60 +
 CHANGELOG.md                                       |   25 +
 CONTRIBUTING.md                                    |   22 +
 README.md                                          |  450 +--
 cmd/kilasflow/main.go                              |   64 +-
 config.example.yaml                                |   47 +-
 docs/src/content/docs/concepts/architecture.md     |   86 +
 docs/src/content/docs/concepts/execution-model.md  |   17 +-
 docs/src/content/docs/concepts/node-registry.md    |    5 +-
 .../src/content/docs/concepts/safety-boundaries.md |   99 +-
 docs/src/content/docs/concepts/webhooks.md         |   33 +
 docs/src/content/docs/guides/community-nodes.md    |    6 +-
 docs/src/content/docs/guides/n8n-migration.md      |  212 +-
 .../content/docs/operate/acceptance-capstone.md    |    3 +-
 .../docs/operate/configuration-reference.md        |   99 +-
 docs/src/content/docs/operate/deployment.md        |   11 +
 docs/src/content/docs/reference/api-contract.md    |   16 +-
 docs/src/content/docs/reference/api.md             |    2 +-
 docs/src/content/docs/reference/api/events.md      |   14 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   17 +-
 e2e/fixtures/n8n-live.ts                           |    1 +
 e2e/helpers/seed.ts                                |   27 +
 e2e/helpers/server.ts                              |    4 +
 e2e/tests/editor-chat.spec.ts                      |   68 +-
 e2e/tests/js-code.spec.ts                          |  695 +++++
 e2e/tests/n8n-compare.spec.ts                      |   22 +-
 e2e/tests/node-coverage.spec.ts                    |   25 +-
 gflow-prd-v1.md                                    |    8 +-
 go.mod                                             |   10 +-
 go.sum                                             |   22 +-
 internal/ai/openai.go                              |   77 +-
 internal/ai/openai_test.go                         |   66 +
 internal/api/handlers/execution_console_test.go    |   62 +
 internal/api/handlers/executions.go                |   50 +-
 internal/api/handlers/executions_events_test.go    |   65 +
 internal/api/handlers/workflows.go                 |    7 +-
 internal/config/config.go                          |  103 +-
 internal/config/config_test.go                     |  105 +
 internal/database/workflow_actor_migration_test.go |   27 +-
 internal/engine/approval.go                        |    2 +-
 internal/engine/authenticate.go                    |    1 +
 internal/engine/checkpoint.go                      |   13 +-
 internal/engine/console_test.go                    |  397 +++
 internal/engine/item_outcomes.go                   |   99 +
 internal/engine/item_outcomes_test.go              |   98 +
 internal/engine/runindex_skip_test.go              |  194 ++
 internal/engine/runindex_test.go                   |   70 +
 internal/engine/runner.go                          |  200 +-
 internal/engine/service.go                         |    4 +-
 internal/engine/wait_service.go                    |    2 +-
 internal/execution/records.go                      |    7 +
 internal/guardrails/jsruntime_boundary_test.go     |  321 ++
 internal/interop/n8n/code_import_test.go           |  115 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   23 +-
 internal/interop/n8n/n8n.go                        |   28 +-
 internal/interop/n8n/n8n_test.go                   |   40 +-
 internal/interop/n8n/parameters.go                 |  102 +-
 internal/jsrun/analyze.go                          |  551 ++++
 internal/jsrun/analyze_test.go                     |  170 ++
 internal/jsrun/bounds_test.go                      |  281 ++
 internal/jsrun/buffer_test.go                      |   41 +
 internal/jsrun/clock.go                            |   57 +
 internal/jsrun/codec.go                            |  183 ++
 internal/jsrun/codec_internal_test.go              |   55 +
 internal/jsrun/console.go                          |   64 +
 internal/jsrun/console_test.go                     |  103 +
 internal/jsrun/crypto.go                           |  859 ++++++
 internal/jsrun/crypto_test.go                      |  792 +++++
 internal/jsrun/doc.go                              |   77 +
 internal/jsrun/engine.go                           |  679 +++++
 internal/jsrun/engine_nodejs.go                    |   34 +
 internal/jsrun/engine_timers.go                    |   85 +
 internal/jsrun/errors.go                           |  179 ++
 internal/jsrun/export_test.go                      |   79 +
 internal/jsrun/guards_test.go                      |  190 ++
 internal/jsrun/intl.go                             | 3109 ++++++++++++++++++++
 internal/jsrun/intl_test.go                        |  580 ++++
 internal/jsrun/items.go                            |  207 ++
 internal/jsrun/js/modules/buffer.js                |  296 ++
 internal/jsrun/js/modules/crypto.js                |  811 +++++
 internal/jsrun/js/modules/intl.js                  |  532 ++++
 internal/jsrun/js/modules/luxon.js                 |  110 +
 internal/jsrun/js/modules/timers.js                |   75 +
 internal/jsrun/js/modules/url.js                   |   10 +
 internal/jsrun/js/modules/util.js                  |   62 +
 internal/jsrun/js/modules/web.js                   |  245 ++
 internal/jsrun/js/runtime.js                       | 1575 ++++++++++
 internal/jsrun/jsrun.go                            |  224 ++
 internal/jsrun/jsrun_test.go                       |  529 ++++
 internal/jsrun/libraries.go                        |   86 +
 internal/jsrun/luxon_test.go                       |  163 +
 internal/jsrun/modules.go                          |   68 +
 internal/jsrun/modules_test.go                     |   50 +
 internal/jsrun/natives.go                          |  113 +
 internal/jsrun/norace_test.go                      |    7 +
 internal/jsrun/programs.go                         |  141 +
 internal/jsrun/race_test.go                        |    8 +
 internal/jsrun/roots.go                            |  126 +
 internal/jsrun/roots_test.go                       |  466 +++
 internal/jsrun/run.go                              |  365 +++
 internal/jsrun/testdata/parity/collation.json      |   10 +
 internal/jsrun/testdata/parity/currencies.json     |   59 +
 internal/jsrun/testdata/parity/date-options.json   | 1804 ++++++++++++
 internal/jsrun/testdata/parity/dates.json          |   22 +
 internal/jsrun/testdata/parity/luxon.json          |   61 +
 internal/jsrun/testdata/parity/numbers.json        |  317 ++
 internal/jsrun/testdata/parity/weeks.json          |   28 +
 internal/jsrun/testdata/parity/zones.json          |  604 ++++
 internal/jsrun/watchdog.go                         |  117 +
 internal/jsrun/web_test.go                         |  202 ++
 internal/jsrun/wire.go                             |  200 ++
 internal/jsrun/wire_test.go                        |   57 +
 internal/jsrun/wrapper.go                          |   81 +
 internal/jsworker/doc.go                           |   53 +
 internal/jsworker/jsworker_test.go                 |  448 +++
 internal/jsworker/limits_linux.go                  |   87 +
 internal/jsworker/limits_other.go                  |   29 +
 internal/jsworker/norace.go                        |    6 +
 internal/jsworker/pool.go                          |  577 ++++
 internal/jsworker/protocol.go                      |  132 +
 internal/jsworker/race.go                          |    6 +
 internal/jsworker/worker.go                        |  133 +
 internal/node/registry.go                          |    8 +-
 internal/repository/executions.go                  |   11 +
 internal/repository/models.go                      |    6 +-
 internal/repository/node_run_console_test.go       |  117 +
 internal/workflow/compiler.go                      |    4 +
 .../postgres/000021_node_run_console.down.sql      |    8 +
 migrations/postgres/000021_node_run_console.up.sql |   25 +
 migrations/sqlite/000021_node_run_console.down.sql |    8 +
 migrations/sqlite/000021_node_run_console.up.sql   |   21 +
 nodes/ai.go                                        |   75 +-
 nodes/ai_test.go                                   |   59 +
 nodes/core.go                                      |    1 +
 nodes/executors.go                                 |   33 +-
 nodes/jscode.go                                    |  257 +-
 nodes/jscode_roots.go                              |   48 +
 nodes/jscode_roots_test.go                         |  123 +
 nodes/jscode_run_test.go                           |  190 ++
 nodes/jscode_test.go                               |   41 +-
 nodes/transform.go                                 |    2 +-
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/js-parity/record.mjs                       |  482 +++
 sdk/src/generated/models.ts                        |  277 ++
 sidecar/doc.go                                     |    2 +-
 sidecar/runner_test.go                             |   15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |    2 +-
 skills/index.json                                  |    2 +
 skills/kilasflow-expressions/SKILL.md              |    5 +-
 skills/kilasflow-import-export/SKILL.md            |   15 +-
 .../references/MAPPING_LIMITS.md                   |   57 +-
 third_party/lodash/LICENSE                         |   47 +
 third_party/lodash/PROVENANCE.md                   |   32 +
 third_party/lodash/embed.go                        |   15 +
 third_party/lodash/lodash.min.js                   |  136 +
 third_party/luxon/LICENSE                          |    7 +
 third_party/luxon/PROVENANCE.md                    |   34 +
 third_party/luxon/embed.go                         |   15 +
 third_party/luxon/luxon.min.js                     |    1 +
 third_party/waha/PROVENANCE.md                     |    5 +-
 web/messages/en/editor.json                        |   20 +-
 web/messages/en/executions.json                    |    2 +
 web/messages/id/editor.json                        |   20 +-
 web/messages/id/executions.json                    |    2 +
 .../api/generated/models/aIAgentCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |   24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |   24 +
 .../api/generated/models/aIModelStartedEvent.ts    |   24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |   24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |   24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |   24 +
 .../lib/api/generated/models/codeConsoleEvent.ts   |   24 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 web/src/lib/api/generated/models/index.ts          |   11 +
 web/src/lib/api/generated/models/otherEvent.ts     |   24 +
 .../models/streamExecutionEvents200Item.ts         |   99 +
 .../api/generated/models/webhookResponseEvent.ts   |   24 +
 .../workflow-editor/canvas-chat-panel.svelte       |  381 ++-
 .../workflow-editor/chat-markdown.svelte           |   38 +
 .../workflow-editor/workflow-editor.svelte         |   54 +-
 web/src/lib/workflow-editor/catalog.test.ts        |   12 +
 web/src/lib/workflow-editor/catalog.ts             |   10 +-
 web/src/lib/workflow-editor/chat-markdown.test.ts  |  110 +
 web/src/lib/workflow-editor/chat-markdown.ts       |  211 ++
 web/src/lib/workflow-editor/chat-stream.test.ts    |   70 +
 web/src/lib/workflow-editor/chat-stream.ts         |  112 +
 web/src/lib/workflow-editor/chat.test.ts           |   54 +-
 web/src/lib/workflow-editor/chat.ts                |   78 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   45 +-
 .../lib/workflow-editor/execution-watch.test.ts    |   58 +
 web/src/lib/workflow-editor/execution-watch.ts     |   56 +
 web/src/lib/workflow-editor/validation.ts          |    5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   36 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   55 +
 394 files changed, 40360 insertions(+), 783 deletions(-)
```
