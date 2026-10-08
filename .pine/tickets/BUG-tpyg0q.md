---
id: BUG-tpyg0q
title: a $fromAI inside an expression's literal text is required in the tool schema but never filled
status: done
priority: low
labels:
    - ai
    - datastore
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-10-08T16:03:39Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

Building a tool's schema scans every string in its parameters for `$fromAI(...)` calls (`collectFromAI`/`scanFromAICalls`, internal/ai/fromai.go ~87-124), regardless of whether the call sits inside an actual expression marker (`{{ ... }}`) or in plain literal text. That makes any `$fromAI` call "required" in the schema the model sees, wherever it's written.

Filling it back in is narrower: `substituteFromAITemplate` and `renderFromAISegments` (internal/ai/fromai.go ~422-482) substitute a call when it's the template's sole content, or when it sits inside a `{{ }}` segment. A `$fromAI(...)` call written as plain literal text with no `{{ }}` around it — for example `{"mode":"expression","value":"Customer $fromAI('name')"}` — is not itself wrapped as one bare `{{ $fromAI('name') }}` marker, so the paths that actually resolve and splice a value do not treat it as fillable in the way the schema already promised.

Concretely: the parameter value stores literally `"Customer $fromAI('name')"`, unresolved, while the tool schema still lists `name` as a required argument the model must supply.

# Steps to Reproduce

1. Build a tool parameter shaped as `{"mode":"expression","value":"Customer $fromAI('name')"}` (a `$fromAI` call inside literal text, not inside `{{ }}`).
2. Inspect the generated tool schema — `name` is listed as required.
3. Run the tool with the model supplying `name`.
4. Inspect the stored/used parameter value.

# Expected

Either such a placement is refused at save (so a pack/workflow author can't create an unfillable required argument), or it is left out of the schema entirely (so the model is never asked for something that won't be filled).

# Actual

The schema requires `name` (from the schema-building scan, which looks at literal text too), but the value is stored/used as literal, unsubstituted text.

# Acceptance Criteria
- [x] A `$fromAI` call written outside a `{{ }}` expression marker in an expression-mode field is either refused at save, or excluded from the tool schema's required arguments
- [x] A test covers the example above: `{"mode":"expression","value":"Customer $fromAI('name')"}` no longer both requires `name` and leaves it unfilled

# Related Files

internal/ai/fromai.go `collectFromAI`/`scanFromAICalls`, ~87-124 (schema-building scan — finds calls anywhere in text)
internal/ai/fromai.go `substituteFromAITemplate`/`soleFromAICall`/`renderFromAISegments`, ~422-482 (fill path — only resolves a call that is the template's sole content or sits inside `{{ }}`)

## Fix

**Root cause.** The schema scan (`collectFromAI`) read an expression marker's whole `value` text as if all of it were code, so it also collected calls in the literal text around the `{{ }}` segments. The fill side treats an expression differently. `SubstituteFromAI` now leaves expressions exactly as written (the `substituteFromAITemplate` / `soleFromAICall` helpers named above no longer exist). The evaluator then runs only the `{{ }}` segments, with the model's arguments in `Context.FromAIArguments`. So a call in literal text was required by the schema and never filled. In a plain string a call is filled wherever it sits, so the two sides already agreed there.

**Change (option 2: left out of the schema).**
- `internal/expression/expression.go`: new `Bodies(template)` returns the source of each `{{ }}` segment, split exactly as `Evaluate` splits it (the same `split` / `closingBraces`, so `}}` inside a string or an object literal does not end a segment early).
- `internal/ai/fromai.go`: `collectFromAI` reads an expression marker through `expression.Bodies` and scans only the segment bodies. Plain strings are scanned whole, as before. A template whose `{{` never closes contributes nothing: the evaluator runs none of it, and it fails with its own error when the tool resolves it (the compiler already reports that at save).
  - `ExtractFromAI`, `CheckFromAIConsistent` and every tool schema (HTTP, Workflow, Calculator, Data table, node-as-tool) go through this one scan.

**Why not refuse at save.** That would make an existing or imported workflow that carries such text fail validation. n8n's own extraction has the same blind spot, so an n8n import could hit it. Leaving the call out of the schema fixes the false "required" claim without blocking anything. The text stays literal, as the evaluator already rendered it.

**Tests.**
- `internal/ai/fromai_test.go` `TestExtractFromAIReadsAnExpressionOnlyInsideItsBraces`. It checks that:
  - `{"mode":"expression","value":"Customer $fromAI('name')"}` yields no argument and nothing required
  - calls inside `{{ }}` (including one inside an object literal) and in a plain string are still collected, while a literal-text call beside them is not
  - the plain string is filled.
- `nodes/ai_fromai_data_test.go` `TestAToolDoesNotAskForAFromAICallInAnExpressionsLiteralText`. It runs the ticket's example end to end through a Workflow Tool. The offered schema requires only `city` (the `{{ $fromAI('city') }}` beside it) and has no `name`. The call runs (before, it failed with `tool argument "name" was not supplied`). The sub-workflow gets `city: "Utrecht"` and `greeting: "Customer $fromAI('name')"` as written.
- `go test ./internal/ai/... ./internal/expression/... ./nodes/... ./internal/engine/... ./internal/repository/... ./internal/interop/...` pass, and `go vet ./...` is clean.
