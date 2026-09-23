---
id: BUG-j7qrp2
title: '`debug eval` evaluates against the node''s output, can''t pick an item/run, and mixes runs'
status: todo
priority: medium
labels:
    - cli
    - debugging
    - expressions
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

n8n evaluates an expression against the node's *input* item. `debug eval` uses the output, and in a loop it answers `$json`, `$input.all()` and `$runIndex` from three different runs.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-20). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Expressions in a node's parameters resolve against that node's input item; the NDV preview steps through items and runs.

# Steps to Reproduce

1. `kilasflow debug eval --execution exec_01a0cbc3-492c-7451-a2a5-44d8a7b62d5c --node failure '$json'`. 2. On the loop run exec_01a0cbc8-07bb-…: `--node tag '{{ $json }}'`, `'{{ $input.all() }}'`, `'{{ $runIndex }}'`.

# Expected

`--node` evaluates as the node's parameters did, with `$json` = input item, plus `--item N` and `--run N` (defaulting to the last run with data) and consistent `$runIndex`/`$itemIndex`.

# Actual

(1) returns Failure's own output (`{"e":"{…}"}`), not the error item it received, so a user cannot reproduce "what did this parameter see". (2) `$json` = `{"tagged":"item 5"}` (the last non-empty run), `$input.all()` = `[]` (the pruned run 3) and `$runIndex` = `0`: three answers from three different runs. There is no `--item` or `--run` flag, so `$('X').item` always fails for multi-item nodes (`node "Two urls" produced 3 items; use .all(), .first() or .last()`). The flag help says "node whose output the expression is evaluated against", which is not what an n8n user expects.

# Acceptance Criteria
- [ ] `--node` evaluates the way the node's parameters did: `$json` is the input item
- [ ] `--item N` and `--run N` flags, defaulting to the last run with data, with consistent `$runIndex`/`$itemIndex`
- [ ] `$('X').item` works when the chosen item's lineage identifies it

# Implementation Plan

Build roots from the chosen run's input item, pass its pairedItem as the current origin, and add item and run selection to the API and CLI.

# Notes

Related (from the audit): none

# Related Files

CLI outputs above, internal/engine/eval.go:163-215 (`roots.JSON = entry.JSON` from the output, `inputs[run.NodeID]` from the last run).

# Attachments
