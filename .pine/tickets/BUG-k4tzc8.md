---
id: BUG-k4tzc8
title: A PostgreSQL/MySQL select whose conditions were edited in the editor ran without its WHERE clause
status: done
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T13:33:29Z"
updated: "2026-10-09T13:33:29Z"
---

# Description

The editor's conditions control writes a PostgreSQL / MySQL v2 node's `where` in its own filter shape (`{combinator, conditions: [{leftValue, operator: {operation}, rightValue}]}`), but the SQL executor only read a plain list of `{field, operator, value}` rows. A select (or delete) whose conditions were edited in the editor therefore ran **without its WHERE clause**. The n8n importer was unaffected (it writes the list shape).

Found and fixed by the Track C coder while hardening condition handling for the agent tool forms (2026-10-09), recorded here for traceability.

# Steps to Reproduce

1. In the editor, add a PostgreSQL v2 node, operation Select, and add two conditions with "or".
2. Run it.

# Expected

`SELECT * FROM "orders" WHERE "status" = $1 OR "total" > $2`.

# Actual (before the fix)

`SELECT * FROM "orders"` — every row.

# Acceptance Criteria
- [x] The executor reads the editor's filter shape, combinator included; a test asserts the two-row "or" filter builds `WHERE "status" = $1 OR "total" > $2` (nodes/tool_form_run_test.go, `TestConditionShapesAreReadOrRefusedNeverIgnored`), merged with Track C (commit a3e5338c).
- [x] Any other non-list `where` on select/delete is refused instead of running unfiltered.

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `ccf8eebd` (last commit at or before ticket created 2026-10-09)
- Commits (1):
  - `ccf8eebd` — chore(pine): BUG-k4tzc8 records the editor-filter WHERE bug Track C fixed
- Files changed (base → working tree):

```
 .pine/tickets/BUG-f8b8gh.md | 21 +++++++++++++++++++--
 1 file changed, 19 insertions(+), 2 deletions(-)
```
