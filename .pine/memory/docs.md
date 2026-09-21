---
topic: docs
updated: 2026-09-21T13:40:19Z
---

# docs

- 2026-09-21: Hand-written docs pages that state a count (api-contract.md operation totals) or a claim no -check target regenerates drift silently: every landing that changes the served surface must grep the hand-written pages too.
- 2026-09-21: A new Huma operation is not documented when generate-api-reference.mjs maps it: also add the row to api-contract.md AND bump internal/cli contractRowCount. generate-api-reference fails on unmapped operation ids; TestAPIEscapeHatchWalksTheContract fails when the page grew a row but the constant stayed behind. (cites: scripts/generate-api-reference.mjs, docs/src/content/docs/reference/api-contract.md, internal/cli/openapi_contract_test.go)
