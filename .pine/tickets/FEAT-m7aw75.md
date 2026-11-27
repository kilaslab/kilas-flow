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
- [x] A tenant-scoped variables table with CRUD endpoints and a Settings → Variables page
- [x] `$vars` is populated in every expression context
- [x] Until that lands, import reports flag `$vars` usage, and evaluation errors instead of returning undefined silently (superseded: the store landed, so `$vars` now has a source)

# Implementation Plan

Add a tenant-scoped variables table with CRUD endpoints and a settings page, and populate Vars when building the expression context. Until then, flag `$vars` in import reports.

# Notes

Related tickets: BUG-4053h6

Related (from the audit): BUG-4053h6 (done; it covered $vars syntax, not the data source).

# Related Files

internal/expression/expression.go:46-50 (the Vars comment says "Empty when the runtime…"); no assignment sites.

# Attachments

## Progress

2026-11-27, on `feat/epic-sweep-2026-11-27`:

- Storage: migration 000033 `variables` (tenant_id, key, value; unique per tenant and key), `repository.GORMVariableStore` (list, create, update, delete, `VariablesFor`). Keys follow n8n's rule `^[A-Za-z_][A-Za-z0-9_]*$` (at most 64 characters), values are plain text up to 4096 bytes, at most 500 per tenant. The table is purged with the tenant (tenant_rows.go).
- API: `list-variables`, `create-variable`, `update-variable`, `delete-variable` under `/api/v1/variables` (409 on a duplicate key, 422 on an invalid one, 404 across tenants). Scope middleware: unbound scoped keys read with `workflow:read` and write with `workflow:write`; embed sessions and workflow-bound keys are refused, like `/schedules`. The generated embed permit table says so.
- Engine: `ServiceDeps.Variables` (a `VariableSource`) is read once per execution into `Request.Vars`, which `ExpressionContext` exposes as `$vars` and the Code node's roots expose too (internal/jsrun). A failed read fails the run instead of running with an empty `$vars`. Trace evaluation (`EvaluateExpression`) reads the current variables.
- Web: Settings → Variables section (add, edit, delete), copy in en and id.
- Docs: reference/expression-grammar.md "Variables", the Code node guide, the API contract page and the generated reference (new Variables group).
- Tests: repository (tenant scoping, validation, both drivers), engine (a run reads its tenant's variables; an unreadable store fails the run), API (CRUD, 409/422/404), scope and embed middleware tables, jsrun `$vars`.
- Not done: no `kilasflow variable` CLI verbs (`kilasflow api list-variables` works), and no importer note, because `$vars` now resolves.
