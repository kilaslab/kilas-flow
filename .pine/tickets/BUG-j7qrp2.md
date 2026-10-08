---
id: BUG-j7qrp2
title: '`debug eval` evaluates against the node''s output, can''t pick an item/run, and mixes runs'
status: done
priority: medium
labels:
    - cli
    - debugging
    - expressions
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-10-08T16:08:57Z"
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
- [x] `--node` evaluates the way the node's parameters did: `$json` is the input item
- [x] `--item N` and `--run N` flags, defaulting to the last run with data, with consistent `$runIndex`/`$itemIndex`
- [x] `$('X').item` works when the chosen item's lineage identifies it

# Implementation Plan

Build roots from the chosen run's input item, pass its pairedItem as the current origin, and add item and run selection to the API and CLI.

# Notes

Related (from the audit): none

# Related Files

CLI outputs above, internal/engine/eval.go:163-215 (`roots.JSON = entry.JSON` from the output, `inputs[run.NodeID]` from the last run).

# Attachments

## Fix (2026-10-08)

`EvaluateExpression` takes an `engine.ExpressionTarget{NodeID, Run, Item}` (internal/engine/eval.go). With a node named it rebuilds the context the node's parameters were resolved with:

- **Run.** `pickRun` numbers the node's runs as `$runIndex` does: its trace rows less pruned deliveries (status skipped) and less attempts a retry replaced. The stored row's `runIndex` is not used for this because it also counts pruned deliveries. With no run given it picks the last run whose input carried items, so the pruned tail row from the ticket's loop no longer decides the answer.
- **Item.** `pickItem` reads the run's `main` input, or the first named port that carried items. It defaults to item 0. A run that received nothing reads as `{}` at index 0, the same item an executor resolves with.
- **Roots, all from one run and one item.** `$json` is the input item and `$input` is that run's input. `$runIndex` and `$itemIndex` are the picked numbers. Every other node is read only from the trace rows before the picked run, which is what `$('X')` saw then. `NodeItems` goes through `PairNodeItems` with the item's stored `pairedItem`, so `$('X').item` follows lineage. The revision is compiled once to get node definitions (port names for the origin pointer) and connected outputs. Parameters stay out of the context.
- **Refusals.** A run or item past the end returns "node X ran N times, so there is no run R" or "run R of node X received N items, so there is no item I". Giving a run or an item without a node is also refused.

**API.** `POST /executions/{id}/eval` takes optional `run` and `item` (minimum 0). The `nodeId` and operation descriptions now say the node's input is what gets evaluated.

**CLI.** `kilasflow debug eval` has `--run <n>` and `--item <n>`. Both are optional whole numbers, and an explicit 0 is sent. Giving either without `--node` is a usage error, and so is a negative or non-numeric value. The flag value is a `flag.Getter`, so MCP publishes them as integers. The `--node` help no longer says "output".

**Regenerated:** the skills command reference (`make generate-skills-command-reference`), the SDK types (`make generate-types`), the web API client (`make generate-api`) and the docs API reference (`make generate-api-reference`). All four `*-check` targets pass, and so does `make skills-check`. The hand-written docs/reference/cli.md debug section was updated.

**Tests.** `TestEvaluateExpressionAtANodeAnswersWhatItsParametersSaw` runs a real workflow where Tag is fed by three branches (3 items, then 1 item, then a pruned delivery). It records the runtime's own answers for `$json`, `$runIndex`, `$itemIndex`, `$input.all()` and `$('Start').item` at every run and item, then requires the evaluator to give the same answers from the stored trace, explicitly and by default. Before the fix it failed with the ticket's `node "Start" produced 3 items` error. Also added: `TestEvaluateExpressionCountsARetryAsOneRun`, `TestEvaluateExpressionRefusesARunOrItemTheNodeDidNotHave`, run/item cases in the API eval test, and `TestDebugEvalSendsTheRunAndTheItem` and `TestDebugEvalPicksARunOrItemOnlyWithinANode` in the CLI. The old cases that asserted `$json` was the node's output were rewritten to assert the input item.

**Note.** `--node` still takes a node id, as before. To read what a node produced, leave `--node` out and use `$('Name')`.
