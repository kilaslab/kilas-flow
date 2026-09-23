---
id: BUG-t12ffz
title: Data table Tool set to Insert (or any write) silently performs a read, and the agent reports success
status: todo
priority: high
labels:
    - ai
    - ai-tools
    - datastore
    - data-integrity
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-09-23T01:56:13Z"
---

# Description

The agent says "successfully added" while the row count stays the same. BUG-6jvcs5 is marked done with this item unchecked.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama; finding ids: AI-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** A Data Table node attached as a tool performs its configured operation (insert, update, upsert, delete, get), and `$fromAI()` fills the column values.

# Steps to Reproduce

1. Create datastore `[ai-ollama] customers` (name, city, plan) with 3 rows.
2. Agent + datastoreTool `add_customer` with `operation: insert` and columns `name/city/plan = {{ $fromAI(...) }}`, plus a second datastoreTool `find_customers` (get).
3. Ask "Add a new customer: Joko Widodo from Yogyakarta on the free plan."

# Expected

Write operations run with `$fromAI` columns, or the tool refuses every operation except get at save/import time and the UI hides them.

# Actual

The model is offered the read schema (`match/conditions/limit`), so it calls `add_customer` with `conditions:[name eq Joko…, city eq …, plan eq free]`. The default `match:"any"` returns Budi's row, and the agent says "The customer "Joko Widodo" … has been successfully added." The second run returned `{"rows":[]}`, and the agent still said "I've added a new customer row for Dian Sastro". Row count stays 3. Status: succeeded. The n8n importer carries `operation: insert` onto the tool with no warning.

# Acceptance Criteria
- [ ] Insert, update, upsert and delete run with `$fromAI` column values and change the table
- [ ] Or, until writes are supported, every operation except get is refused at save and import time and hidden in the UI
- [ ] A test asserts the row count changes after an agent insert

# Implementation Plan

Implement insert/update/upsert/delete in the tool, with a schema derived from the mapped columns or `$fromAI`. Until then, reject non-get operations in `validateDatastoreToolConfiguration` and flag them as blocking on import.

# Notes

Related tickets: BUG-6jvcs5

Related (from the audit): BUG-6jvcs5 (status done). Its checklist item "Data table Tool offers every operation … but always performs a filtered read" is still unchecked and was never addressed in its progress notes. It still reproduces.

# Related Files

`case-5-1.execution.json`, `case-5-2.execution.json`, `case-import.response.json` (no issue for "Add customer" operation insert). Code `nodes/datastore.go:1123-1265`: `Definition`/`Invoke` ignore `operation` and `columns`. The node UI is `toolVariantOf(datastoreNode())`, which inherits all 13 operations.

# Attachments
