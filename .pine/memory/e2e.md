---
topic: e2e
updated: 2026-09-20T03:53:01Z
---

# e2e

- 2026-09-20: Running the full e2e suite locally while `ollama serve` is up executes the ai-agent local-model tests; one serial failure there cascades the rest of that group into skip results with no reason, so `make e2e-skip-budget` fails with "skipped with no reason at all" — a local artefact, not a broken gate (CI has no model, so those tests skip carrying the gate reason). (cites: e2e/tests/ai-agent-ollama.spec.ts)
- 2026-09-20: A workflow document parameter is only evaluated as an expression when it carries the explicit marker {mode:'expression', value:'…'}; a bare '{{ … }}' string is data and passes through verbatim. (cites: internal/expression/expression.go)
