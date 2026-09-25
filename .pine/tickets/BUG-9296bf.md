---
id: BUG-9296bf
title: the HTTP Request Tool and Workflow Tool evaluate model-supplied $fromAI text as expressions
status: doing
priority: high
labels:
    - security
    - ai
    - ai-tools
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-09-25T12:32:17Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

`ai.SubstituteFromAI` splices the model's `$fromAI` argument value into the parameter text at the marker it occupies, and the result is then evaluated as an expression. This is safe when the model's value only ever lands as data, but the HTTP Request Tool and the Workflow Tool hand the spliced result to the expression evaluator, so a model-supplied value can itself become expression syntax:

- a verbatim marker for a string-typed call
- template text around a call
- a call spliced into code, e.g. `.toUpperCase()`
- literals whose `}}` confuse segmenting

The model's value is reachable content: `$env` (through the operator's allowlist), an upstream node's output, or `$execution.resumeUrl`/`approvalUrl`. An agent that echoes attacker-controlled text into a `$fromAI` argument can therefore inject expression syntax that the HTTP Request Tool or Workflow Tool then evaluates.

The Data table Tool already avoids this class of bug: `expression.Context.FromAIArguments` evaluates `$fromAI` arguments as data rather than splicing them into expression text (nodes/datastore.go, internal/expression/roots.go `fromAIArgument`).

# Steps to Reproduce

1. Give an agent an HTTP Request Tool or Workflow Tool parameter built from `$fromAI(...)`.
2. Have the model (or a probe standing in for one) supply a value shaped as one of the four cases above.
3. Observe the value evaluated as an expression rather than treated as literal text.

# Expected

`$fromAI` arguments passed to the HTTP Request Tool and the Workflow Tool are evaluated as data, the same way the Data table Tool already does it via `FromAIArguments` — never spliced into text that is then evaluated as an expression.

# Actual

Both tools splice the model's value via `ai.SubstituteFromAI` and evaluate the result, so a model-supplied value can inject expression syntax.

# Acceptance Criteria
- [ ] The HTTP Request Tool evaluates `$fromAI` arguments via `FromAIArguments` (as data) rather than by splicing and evaluating
- [ ] The Workflow Tool does the same
- [ ] Probes from the sprint covering the four shapes above (verbatim marker, template text around a call, a call spliced into code, literals whose `}}` confuse segmenting) are refused or stored as literal text, not evaluated

# Related Files

internal/ai/fromai.go `SubstituteFromAI` (~line 359) and `substituteFromAITemplate`/`renderFromAISegments` (~line 422 onward) — the splice-then-evaluate path
internal/expression/roots.go `fromAIArgument` (~line 299) and `Context.FromAIArguments` — the data-only path the Data table Tool already uses
nodes/datastore.go — the Data table Tool's use of `FromAIArguments`, as the model to follow
