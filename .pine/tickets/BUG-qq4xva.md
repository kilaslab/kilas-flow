---
id: BUG-qq4xva
title: n8n auto-generated fromAI-override comment breaks every $fromAI tool parameter
status: testing
priority: high
labels:
    - ai
    - expression
    - importer
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T00:47:48Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:expression-parity.

---
### n8n's auto-generated `/*n8n-auto-generated-fromAI-override*/` comment breaks every $fromAI tool parameter [find:expression-parity] (high/bug) · area: AI tools / $fromAI · confidence: medium

When a user clicks n8n's 'let the model define this parameter', n8n writes `{{ /*n8n-auto-generated-fromAI-override*/ $fromAI('x', ``, 'string') }}`. KF's $fromAI substitution only collapses a segment that holds nothing but the call. With the comment present, it inlines the model's value but keeps the braces, and the evaluator then rejects the body.

Evidence: internal/ai/fromai.go:412-433 (soleFromAICall requires empty text around the call) -> renderFromAISegments. Proven with a copy of the current internal/ai/fromai.go and internal/expression (work/expression-parity/harness/fromai/main.go): the template above is substituted to `{{ /*n8n-auto-generated-fromAI-override*/ cats }}` and resolving it fails with 'expression must start with a supported root such as $json'. Without the comment it resolves to 'cats'. Not verified end-to-end through an agent run, because Ollama is reserved for the ai-ollama dimension.

n8n behavior: The comment is ignored (it is a JS comment). The parameter is filled with the model-provided value.

Impact: 27 of 52 $fromAI params (7 of 11 $fromAI templates: 2085, 2462, 3135, 3514, 3770, 3790, 6270) fail on every tool call.

Suggested fix: Strip `/* ... */` comments from expression bodies, either in the importer or in both scanFromAICalls and the evaluator's split/parse. Add a regression test with n8n's exact auto-generated form.

Files: internal/ai/fromai.go, internal/expression/expression.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-je4f4t, FEAT-cgm1y3

## Acceptance criteria

- [ ] n8n's auto-generated `/*n8n-auto-generated-fromAI-override*/` comment breaks every $fromAI tool parameter
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
---
## ImporterTail slice — 2026-09-20

Status: `testing`. Commit: `47544b7` (content; swept into a peer's commit).

Landed: `expressionValue` in `internal/interop/n8n/parameters.go` strips n8n's
`/*n8n-auto-generated-fromAI-override*/` marker, so every imported `$fromAI`
parameter keeps a body the evaluator accepts. One place fixes every node type
that carries an n8n expression (tools, HTTP tool, data table tool, agent
parameters). Regression test:
`TestFromAIOverrideCommentIsStripped` uses n8n's exact auto-generated form,
backticks included.

Remaining: an expression typed by hand in KilasFlow's own editor that contains
that comment still reaches the evaluator untouched — that half belongs to
`internal/expression`/`internal/ai` (ExpressionParity, AINodes2; asked).
