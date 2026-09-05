---
id: FEAT-v8k1tc
title: Extend the expression engine to n8n's evaluation semantics
status: done
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:05:01Z"
updated: "2026-09-05T05:05:01Z"
---

## Scope

`internal/expression` parses one grammar and only one: a root beginning with `$`, followed by `.field` and `[index]` or `["key"]` accessors (`parse`, expression.go:212-262). Anything else — an operator, a call, a bare identifier — is a parse error by design, which is the property `FEAT-pn3dtq` shipped and must not be weakened. Within that grammar, four things diverge from n8n hard enough to break imported workflows.

A missing path is a hard error. `lookup` returns `%s is not set` (expression.go:195) where n8n's JavaScript returns `undefined`, so an optional field that is absent on some items fails the whole execution instead of producing an empty value. Real workflows rely on optional fields constantly.

`$node["X"].json.y` does not resolve. The runner registers `request.NodeOutputs[node.Name] = first` where `first` is the item's JSON map (runner.go:244-246, `firstItem` at 257-264), so `$node` maps a name straight onto the item's fields with no `json` wrapper. The n8n form therefore fails on the `.json` step, and there is no way to write the working form in n8n. Nor is there `$('Node')`, which is the syntax the corpus actually uses.

There is no `$now`, no `$today`, no function of any kind, and no `$fromAI`. A date, a string transform or an AI-populated tool parameter cannot be expressed at all.

And the allowlist is duplicated in the client. `web/src/lib/components/workflow-editor/property-field.svelte:67-70` hardcodes `['$json', '$input', '$node', '$env', '$execution', '$itemIndex']` and reports its own error message, so every root added on the server is rejected in the editor until someone remembers this file. `internal/expression/doc.go` compounds it by documenting `{{ $node["Get User"].json.id }}` as an example — a form that has never worked.

The two dialects stay explicit and separate. n8n marks an expression with a leading `=` on a plain string; KilasFlow uses `{"mode":"expression","value":…}` and `IsExpression` (expression.go:44-54) is what makes "is this an expression?" answerable from the document alone. The importer already translates the marker in both directions. Nothing in this ticket adopts the `=` prefix.

## Acceptance criteria

- [x] A missing path resolves to an undefined value rather than failing the execution; a template containing only that expression yields null and a template mixing it with text yields the text with an empty substitution.
- [x] A genuine error — an unsupported root, a malformed expression, an index into a non-list — still fails loudly and names the path.
- [x] `$('Node')` resolves a node by name and exposes `.item` (the paired item), `.first()`, `.last()` and `.all()`, and `$node["X"].json.y` resolves through a `json` wrapper.
- [x] `$now` and `$today` are available and produce values that format and compare correctly.
- [x] A closed allowlist of functions is callable on values; anything not on the list is a parse error, and no call can reach the host, the filesystem, the network, or another tenant's data.
- [x] `$workflow` and `$execution` expose identity, and `$fromAI` resolves inside an AI tool parameter and is a clear error anywhere else.
- [x] The editor's root and function allowlist comes from the server rather than a hardcoded list, so a root added in Go needs no client change.
- [x] `internal/expression/doc.go` documents the grammar that actually exists, with examples that evaluate.

## Implementation Plan

Start with soft-undefined, because it is the highest-value change and the smallest. Introduce an explicit undefined sentinel rather than Go's `nil` — `nil` is already a legitimate JSON null and conflating the two makes "the field is absent" indistinguishable from "the field is null". `lookup` returns the sentinel instead of erroring on a missing key; `Evaluate` maps it to `nil` for a single-expression template and to the empty string in `stringify`. Keep every structural error — wrong root, index into a non-list, unparseable syntax — as a hard failure.

Then the `json` wrapper and node access. Change what the runner puts in `NodeOutputs` from the bare item map to a structure carrying `json` and `binary`, so `$node["X"].json.y` resolves through it. That is a breaking change for any existing workflow written as `$node["X"].y`, so support both: resolve `.json` when present and fall through to the bare field otherwise, and emit a deprecation diagnostic for the bare form. `$('Node')` is a new syntactic form — a root that takes a quoted argument — which is the first extension of `parse` beyond field and index accessors; keep it a special case in the root parser rather than opening the grammar to general calls. `.item` reads the paired-item lineage, which the runner must already be tracking; without lineage, `.item` must fail with "lineage is not available for this node", never fall back to the first item.

Functions come last and stay closed. A registry of named functions with fixed arity, resolved at parse time so an unknown name is a parse error and never a runtime one, evaluated over values the expression already produced. Do not implement a general call expression — `parse` must continue to reject `require('fs')` structurally, and the test that proves that (`TestEvaluateRejectsAnythingThatIsNotDataAccess`) must keep passing unchanged.

`$fromAI` is different in kind: it is not a data read, it is a marker that an AI agent fills a parameter. Resolve it to a descriptor the agent executor consumes, and make it an error in any other context, or a workflow author will use it in an HTTP URL and get something meaningless.

Finally, serve the allowlist. Add roots and functions to the node-types payload or a dedicated metadata endpoint, and make `property-field.svelte` read it. That is an OpenAPI change — `pnpm generate:api` in `web/`, `pnpm generate:types` in `sdk/`. Rewrite `doc.go` in the same commit as the grammar change, not afterwards, since it is already a year of drift behind.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, entry V2-p1-10.
- `internal/expression/expression.go` — `parse`, `lookup`, `rootValue`, `Evaluate`, `stringify`, `IsExpression`.
- `internal/expression/doc.go` — the stale package documentation.
- `internal/engine/runner.go` — `firstItem` and `NodeOutputs`, which define what `$node` can see.
- `nodes/http.go` — `expressionContext`, the one place every executor builds its evaluation context.
- `web/src/lib/components/workflow-editor/property-field.svelte` — the duplicated client-side root allowlist.
- `.pine/tickets/FEAT-pn3dtq.md` — the no-arbitrary-code guarantee this ticket must not weaken.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 14 — the Fixed/Expression toggle, the `fx` gutter marker and the live Result preview. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Outcome

The safety property is unchanged and proven by the same test:
`TestEvaluateRejectsAnythingThatIsNotDataAccess` passes without modification.
The grammar is still not a language — a root, field reads, index reads, and
calls from a closed allowlist. `require('fs')` is not blocked by a denylist; it
cannot be written, because a body that does not begin with a supported root
never parses.

### Soft undefined

An explicit sentinel, not Go's `nil`, exactly as the plan required: `nil` is a
legitimate JSON null, and conflating the two would make "absent" and "present
and null" indistinguishable. A lone expression that resolved to nothing yields
null; mixed with text it substitutes nothing.

Every structural error stays hard, and a new test pins the four kinds:
unsupported root, index into a non-list, field on a non-object, unknown
function.

### Node access

`$node["X"].json.y` now resolves, and so does the bare `$node["X"].y` that
KilasFlow's own docs advertised. The wrapper carries `json` alongside the bare
fields, so neither form breaks. `$('Name')` is a new root form taking a quoted
argument — kept a special case in the root parser rather than opening the
grammar to general calls — with `.item`, `.first()`, `.last()` and `.all()`.

`.item` reads the paired-item lineage from FEAT-9knk67 and **refuses to fall
back to the first item**, failing with the reason instead. Naming a node that
never ran is an error rather than undefined: it cannot be what the author meant.

### Functions

A closed registry resolved at **parse** time, so an unknown name fails when the
workflow is saved rather than on the first item that reaches it. Arity is
checked there too. Arguments are literals only; accepting a nested expression
would make this a general call expression, which is the one thing it must not
become.

### Dates, identity, `$fromAI`

`$now` and `$today` produce a distinct date type rather than a string, so a date
function can refuse a receiver that is not a date. They stringify as RFC 3339,
which both formats readably and compares correctly. The clock is fixed per
evaluation, so two expressions in one parameter tree cannot disagree about the
time — and a test can pin it.

`$fromAI` resolves to a descriptor an agent consumes and is an error anywhere
else, so an author cannot put it in an HTTP URL and get something meaningless.

### The duplicated allowlist

`GET /api/v1/expression-grammar` serves the roots and functions, and the editor
reads it. The hardcoded list in `property-field.svelte` is gone.

One deliberate choice: while the grammar has not loaded, the editor validates
**nothing** rather than falling back to a local list. Guessing with a stale copy
is precisely what this replaces, and the server refuses anything invalid
regardless.

`TestRootsAndFunctionsAreServedNotDuplicated` evaluates every advertised root,
so the list cannot advertise something the server refuses.

### doc.go

Rewritten in the same change, as instructed. It documented
`{{ $node["Get User"].json.id }}` as an example of a form that had never
worked — that example now evaluates, and is covered by a test.
