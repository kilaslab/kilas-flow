---
id: BUG-tpyg0q
title: a $fromAI inside an expression's literal text is required in the tool schema but never filled
status: todo
priority: low
labels:
    - ai
    - datastore
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-09-23T07:35:37Z"
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
- [ ] A `$fromAI` call written outside a `{{ }}` expression marker in an expression-mode field is either refused at save, or excluded from the tool schema's required arguments
- [ ] A test covers the example above: `{"mode":"expression","value":"Customer $fromAI('name')"}` no longer both requires `name` and leaves it unfilled

# Related Files

internal/ai/fromai.go `collectFromAI`/`scanFromAICalls`, ~87-124 (schema-building scan — finds calls anywhere in text)
internal/ai/fromai.go `substituteFromAITemplate`/`soleFromAICall`/`renderFromAISegments`, ~422-482 (fill path — only resolves a call that is the template's sole content or sits inside `{{ }}`)
