---
id: BUG-n9a6bz
title: Node picker search goes stale after the first letter, and Enter inserts a different node than highlighted
status: todo
priority: high
labels:
    - editor
    - node-picker
parent: EPIC-8rbys7
created: "2026-09-23T01:48:14Z"
updated: "2026-09-23T01:48:14Z"
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
- [ ] Groups are keyed uniquely, so there is no `each_key_duplicate` in the console
- [ ] The rows, the footer and the highlight always reflect the current query
- [ ] Enter inserts exactly the highlighted row
- [ ] A component test types e, a, ai, da and me, and asserts the filtered rows and what Enter inserts

# Implementation Plan

Group the ranked results by category first (a Map, preserving first-seen order), or key the group block by index. Add a unit test for a query whose matches interleave categories.

# Notes

Related tickets: BUG-f9frth

Related (from the audit): none (BUG-f9frth fixed a different each_key_duplicate, in the validation list)

# Related Files

SD/07b-picker-ai.png ("ai" with 36 rows but footer "0 of 36"), SD/09-picker-sort-stale.png, SD/repro-picker3.js output. Cause: node-picker.svelte:53-67 `buildRows` starts a new group whenever the category changes in ranked order, so a search whose results interleave categories (AI, Core, AI…) produces two groups with the same key. node-picker.svelte:193 then iterates `{#each rows.groups as group (group.category)}` → each_key_duplicate → Svelte aborts the update.

# Attachments
