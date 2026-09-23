---
id: FEAT-yxhgeh
title: 'JS Code runtime P2: n8n Code-node globals shim (clean-room), modes, return normalisation, console capture'
status: doing
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-7q13t6
parent: EPIC-tjnr1z
phase: p2
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T04:52:24Z"
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
