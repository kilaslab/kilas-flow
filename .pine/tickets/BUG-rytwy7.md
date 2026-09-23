---
id: BUG-rytwy7
title: Datastore row search starts an endless request loop (~2,600 req/s) and freezes the page
status: done
priority: critical
labels:
    - datastore
    - regression
    - performance
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T04:13:59Z"
---

# Description

Typing one character into Search rows sends GET /datastores/{id} and /rows back to back until the user leaves the page, peaking at 2,642 requests per second. This is a self-inflicted DoS that slows every user of the instance. It is a regression from BUG-esb9sh.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-1). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The data table search filters rows once per keystroke, debounced.

# Steps to Reproduce

1. Open /datastores/<id> for a table that has columns and rows (I used "[ux-ops] Leads": 4 columns, 46 rows). 2. Type one character, e.g. "u", into "Search rows". 3. Watch the network log or the server log.

# Expected

One debounced row query per search change, and the page stays interactive.

# Actual

The page goes to "Loading datastore…" and does not come back. The search box unmounts. The browser sends GET /api/v1/datastores/{id} and GET /rows?limit=20&match=any&columnName=email&condition=ilike&value="%u%" back to back without stopping: 23,678 → 25,882 requests in 3 s in the playwright log, with a peak of 2,642 requests in one second in kf-server.log. The loop ends only when you leave the page. It reproduced twice (08:10:57–08:11:05 and 08:11:49–08:11:53). A self-inflicted DoS like this slows every other user of the instance.

# Acceptance Criteria
- [x] The page's load `$effect` is keyed on `id` only (loads wrapped in `untrack`)
- [x] Search runs through its own debounced effect: one row query per settled change
- [x] A refetch does not flip `detailLoading` or unmount the search box
- [x] A component or e2e test types into search and asserts a bounded number of requests

## Progress

Fixed in `web/src/routes/(dashboard)/datastores/[id]/+page.svelte`:
- The load effect is now keyed on `id` only, with `loadDetail`/`load` calls wrapped in `untrack` (matches the pattern at `executions/[id]/+page.svelte:119-127`).
- Added a second effect that reads `search` and `id`, skips its own first (mount) run, and otherwise sets a 250 ms `setTimeout` that calls `untrack(() => load(id))`, returning `clearTimeout` as cleanup.
- Removed the direct `void load(id)` call from the search input's `oninput`, so search only ever triggers a fetch through the debounced effect.
- `loadDetail` now only sets `detailLoading = true` when `detail === null` (first load), so a background refetch (column add/rename/delete, "Try again") no longer flips the skeleton or unmounts the search box.

Proven by a new Playwright test, `e2e/tests/datastore.spec.ts` — "typing into row search is debounced and never loops (BUG-rytwy7)": seeds a datastore with columns and rows, counts GET requests to the detail and `/rows` endpoints across mount + typing "u" + a 2 s settle, and asserts at most 2 `/rows` requests and exactly 1 detail request, plus that the search input keeps its typed value and focus throughout.
- RED: against the pre-fix component, the test captured 46 `/rows` requests fired back-to-back inside the wait window (i.e., a live reproduction of the reported loop) and failed.
- GREEN: against the fixed component, `pnpm exec playwright test tests/datastore.spec.ts` — 9 passed, including this test.
- Also verified: `cd web && pnpm check` (0 errors) and `cd web && pnpm test` (624 passed).

### Fix round 1

Review found the round-1 fix's debounced-search guard (`searchEffectRan`, a one-shot boolean) only
ever skipped the very first mount. SvelteKit reuses the `[id]/+page.svelte` instance across a
param-only navigation (no `{#key id}` wrapper), so on every later `id` change the debounced effect
still queued its own 250 ms `load(newID)` on top of the id-effect's immediate one — a second,
redundant fetch per table switch. Separately, `detail` was never reset on an `id` change, so with
`detailLoading` already `false` from the previous table, the skeleton was skipped and the old
table's name/columns could show until the new `loadDetail` response landed.

Fixed in the same file:
- The debounce guard is now `lastSearchID: string | null`, compared against the current `id` on
  every run, rather than a one-shot flag — an `id` change updates it and skips scheduling a timer
  (the id-effect already loads), while a same-`id` search change still debounces normally.
- The id-effect now resets `detail = null` and `search = ''` before its untracked loads, so a
  datastore switch always re-shows the skeleton and never carries a stale search term into a table
  it was never typed against.

Covered by a new Playwright test — `e2e/tests/datastore.spec.ts`, "a client-side navigation between
two datastores loads the new one once and drops the old one (BUG-rytwy7)": seeds two datastores,
loads the first, then performs a genuine client-side navigation to the second (clicking an injected
in-app `<a>`, since `page.goto()` is a hard reload and going through the `/datastores` list would
destroy and recreate the component instead of reusing it — neither reaches the reused-instance
path). Asserts the second table's name/row show, the first table's name is gone, and exactly one
GET to the second table's `/rows` endpoint was made.
- RED (against the round-1 fix, temporarily restored via `git show HEAD:...`): 2 GET requests to
  the second table's `/rows` — the immediate load plus the redundant debounced one.
- GREEN (fixed): `cd e2e && pnpm exec playwright test tests/datastore.spec.ts` — 10 passed.
- Also verified: `cd web && pnpm check` (0 errors) and `cd web && pnpm test` (624 passed).

# Implementation Plan

Key the effect on `id` only (wrap the loads in `untrack`). Run the search through its own debounced effect that calls load(), and don't flip detailLoading on a refetch.

# Notes

Related tickets: BUG-esb9sh

Related (from the audit): BUG-esb9sh (done). Its commit df6c87c added this search, so this is a regression.

# Related Files

agents/ux-ops/32-datastore-search-loop.png; agents/ux-ops/datastore-loop-log-excerpt.txt (requests per second); kf-server.log lines 17k–61k. Root cause: web/src/routes/(dashboard)/datastores/[id]/+page.svelte:135-140 runs `$effect(() => { if (id) { void loadDetail(id); void load(id); } })`. load() calls listParams() synchronously, before its first await (:181-196). Once search is non-empty, listParams() reads `userColumns`, which derives from `detail`, so the effect starts tracking `detail`. loadDetail() then reassigns `detail` (:148) and sets `detailLoading = true`, which triggers the effect again, and so on forever. With an empty search listParams returns early, so the loop never shows up without a search.

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (2):
  - `8eaa1911` — BUG-rytwy7: datastore row search debounces instead of looping forever
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
 .pine/tickets/BUG-2eryxn.md                        |  49 +++
 .pine/tickets/BUG-2mes2k.md                        |  54 +++
 .pine/tickets/BUG-2n4rfz.md                        |  51 +++
 .pine/tickets/BUG-2z8geh.md                        |  52 +++
 .pine/tickets/BUG-3k12ky.md                        | 163 +++++++
 .pine/tickets/BUG-3qxx0j.md                        |  59 +++
 .pine/tickets/BUG-56qqgx.md                        |  52 +++
 .pine/tickets/BUG-5bgx5c.md                        |  59 +++
 .pine/tickets/BUG-605n21.md                        | 131 ++++++
 .pine/tickets/BUG-66fhea.md                        |  97 +++++
 .pine/tickets/BUG-6d6wbg.md                        |  98 +++++
 .pine/tickets/BUG-6gkd12.md                        | 107 +++++
 .pine/tickets/BUG-719gaz.md                        |  88 ++++
 .pine/tickets/BUG-9dw5me.md                        |  53 +++
 .pine/tickets/BUG-9pmv8y.md                        |  61 +++
 .pine/tickets/BUG-b3p8va.md                        |  61 +++
 .pine/tickets/BUG-b4cb1c.md                        | 119 +++++
 .pine/tickets/BUG-b8bwhw.md                        |  55 +++
 .pine/tickets/BUG-bcahaj.md                        | 100 +++++
 .pine/tickets/BUG-bw2zc1.md                        |  55 +++
 .pine/tickets/BUG-dstsg9.md                        |  54 +++
 .pine/tickets/BUG-e7dwpk.md                        |  51 +++
 .pine/tickets/BUG-ecbq28.md                        | 111 +++++
 .pine/tickets/BUG-epy2se.md                        | 122 ++++++
 .pine/tickets/BUG-g7ffj1.md                        |  50 +++
 .pine/tickets/BUG-hmp85t.md                        |  99 +++++
 .pine/tickets/BUG-j7qrp2.md                        |  52 +++
 .pine/tickets/BUG-mzk0xn.md                        |  35 ++
 .pine/tickets/BUG-n6p7qy.md                        | 101 +++++
 .pine/tickets/BUG-n9a6bz.md                        |  54 +++
 .pine/tickets/BUG-namghh.md                        |  48 +++
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
 .pine/tickets/BUG-rytwy7.md                        |  68 +++
 .pine/tickets/BUG-sgrxhh.md                        |  53 +++
 .pine/tickets/BUG-t12ffz.md                        |  57 +++
 .pine/tickets/BUG-t3p92b.md                        |  94 ++++
 .pine/tickets/BUG-txafja.md                        |  55 +++
 .pine/tickets/BUG-v8ksv8.md                        |  49 +++
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
 .pine/tickets/EPIC-8rbys7.md                       | 192 +++++++++
 .pine/tickets/EPIC-m42s3g.md                       |   2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 478 +++++++++++++++++++++
 .pine/tickets/FEAT-02cj1g.md                       | 102 +++++
 .pine/tickets/FEAT-02zdcq.md                       | 169 ++++++++
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
 .pine/tickets/FEAT-edzr73.md                       | 121 ++++++
 .pine/tickets/FEAT-egm8bf.md                       |  37 ++
 .pine/tickets/FEAT-eqzpzq.md                       | 136 ++++++
 .pine/tickets/FEAT-ez6xtm.md                       |  55 +++
 .pine/tickets/FEAT-f045nj.md                       | 131 ++++++
 .pine/tickets/FEAT-f3hx3a.md                       |  37 ++
 .pine/tickets/FEAT-fpqg78.md                       |  52 +++
 .pine/tickets/FEAT-fqmh01.md                       |  97 +++++
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
 .pine/tickets/FEAT-mj2nek.md                       |  98 +++++
 .pine/tickets/FEAT-mngmn1.md                       |  32 ++
 .pine/tickets/FEAT-mq412g.md                       |  58 +++
 .pine/tickets/FEAT-mxmjt7.md                       | 129 ++++++
 .pine/tickets/FEAT-n010f0.md                       |  33 ++
 .pine/tickets/FEAT-n12211.md                       |  34 ++
 .pine/tickets/FEAT-nch9dg.md                       |   6 +-
 .pine/tickets/FEAT-npc3ge.md                       |  37 ++
 .pine/tickets/FEAT-nq1vsx.md                       |  53 +++
 .pine/tickets/FEAT-p01rcw.md                       |  98 +++++
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
 e2e/tests/datastore.spec.ts                        |  43 ++
 e2e/tests/editor-chat.spec.ts                      |  68 ++-
 gflow-prd-v1.md                                    |   8 +-
 internal/ai/openai.go                              |  77 +++-
 internal/ai/openai_test.go                         |  66 +++
 internal/api/handlers/executions.go                |  46 +-
 internal/api/handlers/executions_events_test.go    |  64 +++
 internal/config/config.go                          |  10 +-
 internal/config/config_test.go                     |  22 +
 internal/engine/approval.go                        |   2 +-
 nodes/ai.go                                        |  75 ++--
 nodes/ai_test.go                                   |  59 +++
 scripts/generate-api-reference.mjs                 |  28 +-
 sdk/src/generated/models.ts                        | 250 +++++++++++
 sidecar/runner_test.go                             |  15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   2 +-
 web/messages/en/editor.json                        |  20 +-
 web/messages/id/editor.json                        |  20 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIAgentFailedEvent.ts |  24 ++
 .../api/generated/models/aIModelCompletedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |  24 ++
 .../api/generated/models/aIModelStartedEvent.ts    |  24 ++
 .../api/generated/models/aIToolCompletedEvent.ts   |  24 ++
 .../lib/api/generated/models/aIToolFailedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIToolStartedEvent.ts |  24 ++
 web/src/lib/api/generated/models/index.ts          |  10 +
 web/src/lib/api/generated/models/otherEvent.ts     |  24 ++
 .../models/streamExecutionEvents200Item.ts         |  90 ++++
 .../api/generated/models/webhookResponseEvent.ts   |  24 ++
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
 .../(dashboard)/datastores/[id]/+page.svelte       |  45 +-
 254 files changed, 14895 insertions(+), 550 deletions(-)
```
