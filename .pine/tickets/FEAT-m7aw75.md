---
id: FEAT-m7aw75
title: '$vars is always empty: add a Variables store (API + Settings page) or flag $vars on import'
status: todo
priority: medium
labels:
    - expressions
    - variables
    - n8n
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

Imported n8n workflows that read `$vars.X` silently get undefined, because `ExpressionContext.Vars` is never assigned.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-23). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Settings → Variables defines key/value pairs, and `{{ $vars.KEY }}` resolves them in every workflow.

# Steps to Reproduce

1. Search for a place to define variables in the UI. There is none. 2. `grep -rn "Vars" internal cmd nodes --include=*.go`.

# Expected

A Variables store (API plus a Settings page) feeding ctx.Vars, or an importer diagnostic plus an expression error saying that `$vars` isn't supported.

# Actual

The expression root `$vars` exists (internal/expression/roots.go:197,246), but `ExpressionContext.Vars` is never assigned anywhere, so imported n8n workflows that read `$vars.X` silently get undefined or empty values. No API or UI exists to manage variables.

# Acceptance Criteria
- [ ] A tenant-scoped variables table with CRUD endpoints and a Settings → Variables page
- [ ] `$vars` is populated in every expression context
- [ ] Until that lands, import reports flag `$vars` usage, and evaluation errors instead of returning undefined silently

# Implementation Plan

Add a tenant-scoped variables table with CRUD endpoints and a settings page, and populate Vars when building the expression context. Until then, flag `$vars` in import reports.

# Notes

Related tickets: BUG-4053h6

Related (from the audit): BUG-4053h6 (done; it covered $vars syntax, not the data source).

# Related Files

internal/expression/expression.go:46-50 (the Vars comment says "Empty when the runtime…"); no assignment sites.

# Attachments
