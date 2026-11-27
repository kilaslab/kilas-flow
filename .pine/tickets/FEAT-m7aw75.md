---
id: FEAT-m7aw75
title: '$vars is always empty: add a Variables store (API + Settings page) or flag $vars on import'
status: done
priority: medium
labels:
    - expressions
    - variables
    - n8n
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-11-27T08:51:27Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-11-27.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (4):
  - `b7d430b5` — FEAT-m7aw75, FEAT-gzd32h: cover variables in the tenant purge, the SDK and the test gates
  - `2ede62cf` — FEAT-m7aw75: move the variable key placeholder into the message catalog
  - `9187afb9` — FEAT-m7aw75: a tenant Variables store behind $vars
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .env.example                      |    8 +-
 .github/assets/editor.png         |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml          |  108 +++-
 .github/workflows/code-corpus.yml |   54 ++
 .gitignore                        |    4 +
 .pine/MEMORY.md                   |    2 +
 .pine/memory/code-node.md         |   30 +-
 .pine/memory/docs.md              |    5 +-
 .pine/memory/e2e.md               |    3 +
 .pine/memory/licensing.md         |   15 +-
 .pine/memory/n8n-reference.md     |   10 +-
 .pine/memory/persistence.md       |    6 +-
 .pine/memory/testing.md           |    4 +-
 .pine/memory/web-editor.md        |    7 +-
 .pine/roadmap.md                  |    8 +-
 .pine/tickets/BUG-0592hz.md       |   62 ++
 .pine/tickets/BUG-0bzsa1.md       |  464 +++++++++++++++
 .pine/tickets/BUG-0grt9g.md       |  427 +++++++++++++
 .pine/tickets/BUG-0xv7bg.md       |   68 +++
 .pine/tickets/BUG-14gp8r.md       |  198 +++++++
 .pine/tickets/BUG-15st2k.md       |  198 +++++++
 .pine/tickets/BUG-1vh6k6.md       |   51 ++
 .pine/tickets/BUG-2d1pdx.md       |   49 ++
 .pine/tickets/BUG-2eryxn.md       |   95 +++
 .pine/tickets/BUG-2mes2k.md       |   83 +++
 .pine/tickets/BUG-2n4rfz.md       |   58 ++
 .pine/tickets/BUG-2vcwjf.md       |  126 ++++
 .pine/tickets/BUG-2xrz6c.md       |   51 ++
 .pine/tickets/BUG-2z8geh.md       |  136 +++++
 .pine/tickets/BUG-3k12ky.md       |  187 ++++++
 .pine/tickets/BUG-3mem9s.md       |  105 ++++
 .pine/tickets/BUG-3qxx0j.md       |   70 +++
 .pine/tickets/BUG-46g75c.md       |  219 +++++++
 .pine/tickets/BUG-49vf3j.md       |  428 ++++++++++++++
 .pine/tickets/BUG-4ch186.md       |   85 +++
 .pine/tickets/BUG-548bk9.md       |  179 ++++++
 .pine/tickets/BUG-56qqgx.md       |   88 +++
 .pine/tickets/BUG-5bgx5c.md       |   81 +++
 .pine/tickets/BUG-5dn8hr.md       |   39 ++
 .pine/tickets/BUG-5fhcx7.md       |   50 ++
 .pine/tickets/BUG-5xkexq.md       |   63 ++
 .pine/tickets/BUG-605n21.md       |  154 +++++
 .pine/tickets/BUG-66fhea.md       |  112 ++++
 .pine/tickets/BUG-6d6wbg.md       |  115 ++++
 .pine/tickets/BUG-6gkd12.md       | 1183 +++++++++++++++++++++++++++++++++++++
 .pine/tickets/BUG-719gaz.md       |  117 ++++
 .pine/tickets/BUG-745mh2.md       |   60 ++
 .pine/tickets/BUG-8xz9t1.md       |  511 ++++++++++++++++
 .pine/tickets/BUG-9296bf.md       |  532 +++++++++++++++++
 .pine/tickets/BUG-9dw5me.md       |   66 +++
 ...
 1646 files changed, 275917 insertions(+), 7906 deletions(-)
```
