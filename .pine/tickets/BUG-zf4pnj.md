---
id: BUG-zf4pnj
title: $('Node').item fails after count-changing nodes and error-output splits (BUG-rrkjrd still reproduces)
status: todo
priority: high
labels:
    - engine
    - expressions
    - lineage
    - regression
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

`$('X').item` is the most common cross-node reference in n8n workflows. It still fails downstream of a Code node that changed the item count, and on both branches of a node with an error output.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-9). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** `$('Code').item` in a node after Code → HTTP resolves through the HTTP items' pairedItem to Code item i. It does not matter whether Code's own items trace back to the trigger.

# Steps to Reproduce

1. `[ux-debug] k lineage plain http`: Go Code emits 3 items → HTTP (all 200) → Set `{{ $('Two urls').item.json.u }}`. 2. `[ux-debug] i paired item lineage`: the same with HTTP `continueErrorOutput` → Success/Failure using `$('Two urls').item`. 3. `[ux-debug] j`: `continueRegularOutput`.

# Expected

When the current item's origin names the referenced node itself, `.item` is that node's item at `origin.itemIndex`. The error-output router should keep each item's input pairing.

# Actual

k fails with `node "After regular": … "value": node "Two urls" changed the item correspondence, so there is no single item to pair with`, although every HTTP item is stamped `{"sourceNodeId":"two-urls","itemIndex":i}`. i fails with `… the item being processed lost its lineage upstream, so there is no single item to pair with`, because every item on both outputs of a node with an error output is stamped `lost: true`. j fails the same way as k. `kilasflow debug eval … "{{ $('Two urls').item.json.u }}"` answers `node "Two urls" produced 3 items; use .all(), .first() or .last()`.

# Acceptance Criteria
- [ ] When the current item's origin names the referenced node and run, `.item` resolves to that node's item at `origin.itemIndex`
- [ ] Error-output and continue-on-fail items keep their input item's pairing instead of being stamped `lost`
- [ ] Workflows k, i and j from the audit (Code → HTTP → Set with `$('Two urls').item`) succeed

# Implementation Plan

In `pairNodeItem`, short-circuit when the item's origin is the referenced node and run. Stamp error-output and continue-on-fail items with their input item's origin instead of Lost.

# Notes

Related tickets: BUG-rrkjrd

Related (from the audit): BUG-rrkjrd (done, critical; the same symptom still reproduces for Code-node sources and error branches)

# Related Files

`$SP/agents/ux-debug/cli/run_k.json`, `cli/run_i.json`, `cli/run_j.json`. internal/engine/runner.go:1995-2050 (`pairNodeItem` compares only `ItemOrigins`, which are "" for a node whose own lineage was lost, and never checks `origin.SourceNodeID == name`), :1900-1905 (count-changing nodes marked Lost).

# Attachments
