---
id: BUG-rytwy7
title: Datastore row search starts an endless request loop (~2,600 req/s) and freezes the page
status: todo
priority: critical
labels:
    - datastore
    - regression
    - performance
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
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
- [ ] The page's load `$effect` is keyed on `id` only (loads wrapped in `untrack`)
- [ ] Search runs through its own debounced effect: one row query per settled change
- [ ] A refetch does not flip `detailLoading` or unmount the search box
- [ ] A component or e2e test types into search and asserts a bounded number of requests

# Implementation Plan

Key the effect on `id` only (wrap the loads in `untrack`). Run the search through its own debounced effect that calls load(), and don't flip detailLoading on a refetch.

# Notes

Related tickets: BUG-esb9sh

Related (from the audit): BUG-esb9sh (done). Its commit df6c87c added this search, so this is a regression.

# Related Files

agents/ux-ops/32-datastore-search-loop.png; agents/ux-ops/datastore-loop-log-excerpt.txt (requests per second); kf-server.log lines 17k–61k. Root cause: web/src/routes/(dashboard)/datastores/[id]/+page.svelte:135-140 runs `$effect(() => { if (id) { void loadDetail(id); void load(id); } })`. load() calls listParams() synchronously, before its first await (:181-196). Once search is non-empty, listParams() reads `userColumns`, which derives from `detail`, so the effect starts tracking `detail`. loadDetail() then reassigns `detail` (:148) and sets `detailLoading = true`, which triggers the effect again, and so on forever. With an empty search listParams returns early, so the loop never shows up without a search.

# Attachments
