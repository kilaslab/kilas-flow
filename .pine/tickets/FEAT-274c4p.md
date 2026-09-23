---
id: FEAT-274c4p
title: 'Import-fidelity scoreboard over the top-N n8n templates (baseline: 2/45 runnable as imported)'
status: todo
priority: medium
labels:
    - importer
    - n8n
    - metrics
parent: EPIC-8rbys7
created: "2026-09-23T01:35:50Z"
updated: "2026-09-23T01:35:50Z"
---

# Description

Of the 45 most-viewed n8n templates, every one imports (201), but only 2 (4.4%) are runnable as imported. 25.6% of functional nodes become placeholders. That number should be tracked the way the importer corpus already tracks regressions, so every node ticket in EPIC-8rbys7 visibly moves it.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: n8n-templates; finding ids: TPL-1). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** These are n8n.io's most-viewed templates (1750 "Creating an API endpoint": 422K views; 1954 "AI agent chat": 780K). In n8n each one runs as soon as you attach credentials.

# Steps to Reproduce

1. `python3 agents/n8n-templates/import_all.py`. It POSTs each `templates/<id>.json` to `/api/v1/workflows/import`, then calls `/workflows/{id}/diagnostics` and `/workflows/validate`.
2. Read `import-report.csv`.

# Expected

Most of the popular templates import and run once credentials are attached.

# Actual

- Every import returns 201. However, 43/45 fail `validate`.
- The per-template blocker mix: an unsupported placeholder in 41, JS Code in 17, a non-credential blocking parameter in 13, a silent drop in 9 (TPL-3, TPL-4).
- By node type, the three biggest blockers are the same in both samples:
  | Node type | 45 imported (live) | Top 198 (static) |
  |---|---|---|
  | `n8n-nodes-base.code` | 17 templates | 56 (28%) |
  | `n8n-nodes-base.googleSheets` | 10 templates, 25 nodes | 43 (22%) |
  | `@n8n/n8n-nodes-langchain.openAi` | 8 templates, 18 nodes | 41 (21%) |
- In the static scan, only 22/198 (11%) use node types that are all mapped and runnable. Adding those three types takes that to 43/198 (22%).

# Acceptance Criteria
- [ ] A script, following the gitignored-corpus pattern, imports the top-N templates by views and records per template: import ok, placeholders, JS Code, blocking issues, validate pass, lost edges
- [ ] `BASELINE.md` holds the runnable-as-imported rate and the unsupported-type frequency, and CI fails if the rate regresses
- [ ] The baseline from this audit (2/45 clean, 183/716 placeholders) is the starting point

# Implementation Plan

- Prioritise by the greedy table: Code (the JS sidecar from FEAT-7cg0cd now exists), Google Sheets, and the OpenAI app node's text/chat operations mapped onto chainLlm + lmChatOpenAi.
- Then add the `<type>Tool` variants of nodes that already run natively.
- Fix the importer bugs below (TPL-2/3/4/7). Their patterns occur in 58 of the 198 templates, and they block 6 of the 22 templates whose node types are all supported (2035, 2384, 2436, 2682, 2777, 3078).

# Notes

Related tickets: FEAT-7cg0cd, FEAT-8qyfh1

Related (from the audit): FEAT-8qyfh1 (done). Its "refuse JS Code" decision was predicated on deferring the sidecar, and FEAT-7cg0cd has since shipped it. The same node-level gaps are covered in depth by NG-1, NG-2, NG-4 and NG-14 in `findings/node-gap.md`.

# Related Files

- `agents/n8n-templates/import-report.csv`
- `agents/n8n-templates/unsupported-frequency.md` (tables A and B, greedy unlock order, cheap tool-variant wins)
- `import/blocker_categories.json`
- `internal/interop/n8n/n8n.go:803-817` (placeholder path)

# Attachments
