---
id: BUG-n9a6bz
title: Node picker search goes stale after the first letter, and Enter inserts a different node than highlighted
status: done
priority: high
labels:
    - editor
    - node-picker
parent: EPIC-8rbys7
created: "2026-09-23T01:48:14Z"
updated: "2026-10-08T15:58:00Z"
---

# Description

The node picker is the most-used control in the editor. For most queries the list stops filtering, and Enter inserts a node the user didn't pick. Each stale render throws `each_key_duplicate`.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-1). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The node creator re-filters on every keystroke, and Enter inserts the highlighted row.

# Steps to Reproduce

1. Open any workflow that has a node. Click empty canvas, then press Tab (or use a "+" handle). 2. Type `e` (or `a`, `s`, `ai`, `da`, `me`). 3. Look at the list and the footer. 4. Press Enter.

# Expected

The rows, footer and highlight always reflect the current query, and Enter inserts what the user sees highlighted.

# Actual

The list keeps all 59 unfiltered rows with "AI Agent" highlighted, while the footer disagrees ("0 of 36 nodes" after typing `ai`). Enter then inserts the node that the hidden, correct result ranked first. `e` shows AI Agent highlighted and adds **Embeddings**. `da` adds **Data table Tool**. `me` adds **Merge**. Every stale render throws `Error: https://svelte.dev/e/each_key_duplicate` as an uncaught page error (58 of them in this session). The list recovers only when the query matches a single category ("sort").

# Acceptance Criteria
- [x] Groups are keyed uniquely, so there is no `each_key_duplicate` in the console
- [x] The rows, the footer and the highlight always reflect the current query
- [x] Enter inserts exactly the highlighted row
- [x] A component test types e, a, ai, da and me, and asserts the filtered rows and what Enter inserts

## Verification (2026-10-08)

Fix commit: `c603bee`. This verification also made a small follow-up fix in `web/src/lib/workflow-editor/picker-rows.ts`, described below.

- **Groups keyed uniquely (met).** `buildPickerRows` (`web/src/lib/workflow-editor/picker-rows.ts`) groups the rows by category through a Map, so the keyed `{#each rows.groups as group (group.category)}` at `node-picker.svelte:175` can no longer see a duplicate key. Test: `picker-rows.test.ts` "emits one group per category even when ranked results interleave categories". In the app, typing e, a, ai, da and me raised 0 page errors, and no console log contained `each_key_duplicate`.
- **Rows, footer and highlight follow the query (met).** The footer counts `rows.flat` (`node-picker.svelte:204`). In the app, "ai" showed 24 rows with the footer "24 of 60 nodes" and AI Agent highlighted, "da" showed 11 rows with "11 of 60", and "me" showed 18 rows with "18 of 60". Each time, the highlighted row was also the first row drawn and the combobox's `aria-activedescendant`.
- **Enter inserts the highlighted row (met).** Enter inserts `rows.flat[activeIndex]` (`node-picker.svelte:114`), and the highlight is `row.index === activeIndex` (`:184`). In the app, Enter inserted exactly the highlighted node for every query: e gave Embeddings, a gave AI Agent, ai gave AI Agent, da gave Data table Tool and me gave Merge (the node count went from 4 to 5 each time).
- **Test for e, a, ai, da and me (met, with one deviation).** `picker-rows.test.ts` "walks the rows in the order the list draws them, for every audited query" types each of the five queries. It asserts the filtered rows in the order they are drawn and the row Enter inserts. The deviation: this is a unit test of `buildPickerRows`, which holds all of the component's row logic, because `web/` has no Svelte component test harness (no `@testing-library/svelte`). The component itself was covered by the playwright run above.

**Follow-up fix made here.** `rows.flat` kept the ranked order while the list draws rows grouped by category, so the arrow keys skipped around whenever the ranking interleaved categories. With "e", the ranked order is Embeddings, Edit Image, AI Agent and so on. ArrowDown from Embeddings jumped to Edit Image (Core, drawn fourth) and skipped AI Agent, which is drawn directly below it. `buildPickerRows` now reads the flat list back out of the groups, so the keyboard walks the drawn order. The first row is still the best-ranked match. After rebuilding, ArrowDown from the first row landed on the second drawn row for all five queries (e: Embeddings to AI Agent, ai: AI Agent to Basic LLM Chain, da: Data table Tool to Default Data Loader, me: Merge to Calculator).

Self-test setup: `make build-web` + `go build ./cmd/kilasflow` from this branch, served on 127.0.0.1:18095 with an isolated SQLite file (`KILASFLOW_DATABASE_DSN=/tmp/kf-verify/data/kilasflow.db`), driven with playwright-cli. Probe workflows were imported through `POST /api/v1/workflows/import`. Screenshots are in `/tmp/kf-verify/` and are not committed. Screenshot: `11-picker-after-enter-ai.png`.

# Implementation Plan

Group the ranked results by category first (a Map, preserving first-seen order), or key the group block by index. Add a unit test for a query whose matches interleave categories.

# Notes

Related tickets: BUG-f9frth

Related (from the audit): none (BUG-f9frth fixed a different each_key_duplicate, in the validation list)

# Related Files

SD/07b-picker-ai.png ("ai" with 36 rows but footer "0 of 36"), SD/09-picker-sort-stale.png, SD/repro-picker3.js output. Cause: node-picker.svelte:53-67 `buildRows` starts a new group whenever the category changes in ranked order, so a search whose results interleave categories (AI, Core, AI…) produces two groups with the same key. node-picker.svelte:193 then iterates `{#each rows.groups as group (group.category)}` → each_key_duplicate → Svelte aborts the update.

# Attachments
