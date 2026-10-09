---
topic: e2e
updated: 2026-09-20T03:53:01Z
---

# e2e

- 2026-09-20: Running the full e2e suite locally while `ollama serve` is up executes the ai-agent local-model tests; one serial failure there cascades the rest of that group into skip results with no reason, so `make e2e-skip-budget` fails with "skipped with no reason at all" — a local artefact, not a broken gate (CI has no model, so those tests skip carrying the gate reason). (cites: e2e/tests/ai-agent-ollama.spec.ts)
- 2026-09-20: A workflow document parameter is only evaluated as an expression when it carries the explicit marker {mode:'expression', value:'…'}; a bare '{{ … }}' string is data and passes through verbatim. (cites: internal/expression/expression.go)
- 2026-10-09: Two traps when checking build output and screenshots: ripgrep skips gitignored paths, so `rg data-force web/build` or `internal/web/dist` reports nothing whether or not the string is there; use `rg --no-ignore` (and check a positive control). And Playwright 1.63 FAILS the run that writes a missing toHaveScreenshot baseline even with updateSnapshots 'missing' (the docs say it passes); the second run passes. The CI visual step is continue-on-error until e2e/visual/__screenshots__/linux/ is committed for that reason. (cites: e2e/playwright.visual.config.ts)
