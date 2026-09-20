---
id: BUG-rrkjrd
title: $('Node').item fails whenever the referenced node produced more than one item
status: testing
priority: critical
labels:
    - expression
    - engine
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T01:02:13Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Note: Grounded via Context7 n8n docs (/n8n-io/n8n-docs): $(name).item = the linked item via per-item thread; fails only when thread broken/ambiguous.

Consolidates 2 finding(s) from dims: find:expression-parity, find:unfinished-work.

---
### $('Node').item fails whenever the referenced node produced more than one item [find:expression-parity] (critical/bug) · area: expression engine / paired-item lineage · confidence: high

`$('X').item` resolves only when node X produced exactly one item. Otherwise it fails the node with "node X produced N items; use .all(), .first() or .last()". The runner records per-item provenance (PairedItem) but the expression context never uses it to pick the item the current item descends from.

Evidence: internal/engine/runner.go:1373-1416 (nodeItemFor) sets Paired only when len(Items)==1. Live test (work/expression-parity/lineage.py, lineage-results.json): Split Out (3 items) -> {Set, Filter, HTTP Request, Loop Over Items, IF, Limit} -> Set `{{ $('Split Out').item.json.v }}`. n8n 2.33.7 resolves a/b/c per item in all of them (Aggregate is an n8n error too). KF fails every topology. Re-verified today with t_lineage.py (Webhook->Seed->Split Out->Set->Probe): status failed with the error above. The same shape is claimed as working in the acceptance criteria of FEAT-9knk67 ('A -> Set -> B over three items ... resolves to item 2 of A'), but TestLineageSurvivesAOneToOneChain only checks the stored provenance and never evaluates an expression.

n8n behavior: `$('Node').item` returns the item of Node that the current item is paired with, following pairedItem back through one-to-one nodes such as Set, IF, Filter, HTTP Request, Sort and Limit, and through loops. It errors only when the pairing is ambiguous.

Impact: `$('Node').item` is the most-used construct after $json: 474 expression params in 59 of 100 templates (roadmap: 32% of corpus expressions). Any of those workflows that handle more than one item (Split Out, loops, lists from APIs, Sheets rows) fail on the first item.

Suggested fix: Pass the current item's PairedItem chain into ExpressionContext (per item, per run index) and resolve `.item` by walking provenance back to the named node. Fail only when lineage is really Lost or ambiguous. Add an end-to-end test that evaluates `$('A').item` on item 2 of B.

Files: internal/engine/runner.go, internal/expression/roots.go, internal/engine/authenticate.go, internal/expression/doc.go

Existing tickets: FEAT-9knk67, FEAT-v8k1tc

---
### $('Node').item still fails whenever Node produced more than one item; FEAT-9knk67's criterion is ticked but not true at runtime [find:unfinished-work] (high/parity-gap) · area: engine / paired-item lineage · confidence: high

The runner now stamps pairedItem provenance on items, but expressions never use it. `.item` only resolves when the referenced node produced exactly one item. Otherwise it fails with '... produced N items; use .all(), .first() or .last()', and the error text also leaks an internal NUL-separated root name.

Evidence: Live: wf_01a0b8e9-4743-775e-bb10-e587ea057ef3 is Manual -> Split Out(list) -> Set 'Map' (w={{$json.v}}) -> Set 'Back' (orig={{ $('Split Out').item.json.v }}), run with input {"list":[{v:1},{v:2},{v:3}]}. Split Out and Map each output 3 items. Back fails with: `$node\x00Split Out.item: node "Split Out" produced 3 items; use .all(), .first() or .last() to choose one`. Code: internal/engine/runner.go:1378-1415 (nodeItemFor) only sets Paired when there is exactly one item, with the comment 'The executor that evaluates an expression per item supplies that; until it does...'. No executor supplies it. internal/expression/doc.go:72-87 also says this is 'not yet possible'. FEAT-9knk67 criterion 30 ('A→Set→B over three items ... resolves to item 2 of A') is marked [x].

n8n behavior: .item follows pairedItem back to the ancestor of the item currently being processed.

Impact: `$('X').item` appears 551 times in 59 of the 100 most-viewed templates. Every reference to a node that produced several items fails at run time, after a clean import and activation.

Suggested fix: Pass the current item's paired chain into the per-item expression context and walk it back to the referenced node's run to pick the matching item. Reopen FEAT-9knk67 and add an end-to-end test for the A→Set→B case. Clean up the error text (no \x00).

Files: internal/engine/runner.go, internal/expression/roots.go, internal/expression/doc.go, nodes/executors.go

Existing tickets: FEAT-9knk67, FEAT-v8k1tc

## Acceptance criteria

- [ ] $('Node').item fails whenever the referenced node produced more than one item
- [ ] $('Node').item still fails whenever Node produced more than one item; FEAT-9knk67's criterion is ticked but no
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress (EngineFlow 2026-09-20, engine/flow slice)

- `$('X').item` now resolves per item. `nodeItemFor` publishes each completed node's whole run with, per item, the canonical origin key it descends from (`expression.OriginKey`, so the runner and the evaluator cannot drift on the format). `engine.PairNodeItems(items, current, itemIndex)` then picks the paired item for the item being evaluated: a unique item descending from the same origin; else a node that produced exactly one item; else the item at the current position (the positional correspondence n8n uses for a same-order chain, which is what a fan-out like Split Out needs); else a refusal carrying the reason. Ambiguity and lost lineage fail with a reason instead of a confident wrong item.
- `nodeItemFor` keeps the single-item pairing so nothing depends on the wiring landing first; `PairNodeItems` recomputes it per item.
- Also filled while there: `NodeItem.Parameters` (`$('X').params`) and `$workflow` identity/name/timezone from the compiled graph (`workflowContextFor`), which was empty in every production run.
- Tests: `TestDollarItemFollowsPairedLineagePerItem` (Manual -> Split Out -> Set -> IF -> Filter -> Limit -> probe reading `$('Split Out').item.json.v`, expects a/b/c per item) and `TestDollarItemFailsRatherThanGuessingWhenLineageIsAmbiguous` (refusal carries a reason, no NUL in the text).
- Scoped proof (isolated worktree at HEAD + only these files): `go test ./internal/engine/ -count=1` ok, `go test ./internal/workflow/ -count=1` ok.
- Remaining: ExpressionParity's one-line wiring in internal/engine/authenticate.go (`NodeItems: PairNodeItems(request.NodeItems, item, index)`), then the end-to-end check through the real node executors (without the manual call the test makes). Not a code-only close: the pre-wiring failure was observed (`TestDollarNodeByNameReachesTheExecutor`: "lineage is not available for this node").

## Progress (EngineFlow 2026-09-20, engine/flow slice) — part 2: end-to-end verified

- ExpressionParity landed the wiring (d26f242, internal/engine/authenticate.go): `NodeItems: PairNodeItems(request.NodeItems, item, index)`. `TestDollarItemFollowsPairedLineagePerItem` now goes through the real `ExpressionContext` (the manual pairing call in the probe is gone) and resolves a/b/c per item across Manual -> Split Out -> Set -> IF -> Filter -> Limit -> probe.
- Scoped proof: `go test ./internal/engine/ -run 'TestDollar|TestLineage|TestExpression' -count=1` ok; full `./internal/engine/` suite ok.
- Known bound, documented in code: the runner stamps each item with the origin it descends from, not every intermediate step, so a chain that both drops and reorders items between X and the current node can only be paired positionally; genuine ambiguity and lost lineage still fail with a reason instead of a guess. The `$node\x00` prefix in the old failure text is gone from the pairing reasons.
