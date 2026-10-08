---
id: BUG-b8bwhw
title: Importer does not map *Tool variants of native nodes (httpRequestTool, gmailTool, postgresTool)
status: todo
priority: medium
labels:
    - n8n
    - importer
    - ai
deps:
    - FEAT-cf5n4j
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-10-08T16:58:14Z"
---

# Description

This blocks n8n's own onboarding template 6270 (its "Get Weather" tool is an httpRequestTool). BUG-6jvcs5 closed with this item unchecked.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-14). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Every usableAsTool node exports as `<type>Tool`. Google Calendar Tool appears in 17 templates, Google Sheets Tool in 16, Gmail Tool in 16, HTTP Request Tool in 9, Postgres Tool in 7 and Telegram Tool in 3. All tool variants together appear in 5.9% of templates, and those whose base node is already native in 3.4%.

# Steps to Reproduce

Import Manual → AI Agent ← OpenAI model + tool X, using real instances: httpRequestTool 6035 (`url`, `sendQuery`, `toolDescription`), gmailTool 4366, googleSheetsTool 19348.

# Expected

`<mapped base>Tool` maps onto the KilasFlow tool variant, using the base node's parameter translator plus `toolDescription`.

# Actual

- All are blocking placeholders ("KilasFlow has no equivalent of the n8n node \"n8n-nodes-base.httpRequestTool\"…"), even though `kilasflow.httpTool`, `kilasflow.gmail` and `kilasflow.postgres` exist.
- This blocks n8n's own onboarding template 6270 ("Get Weather" httpRequestTool).

# Acceptance Criteria
- [x] A generic rule maps `<type>Tool` onto the KilasFlow tool variant whenever the base node is mapped, carrying `toolDescription`
- [ ] httpRequestTool, gmailTool, postgresTool and telegramTool import and run as agent tools
- [ ] Template 6270 imports with no blocking issue (its Get Weather httpRequestTool now imports and compiles on an agent; asserted on a fixture shaped like that tool, not on the template itself, which is not in the repo, so the template's other nodes are unverified)

# Implementation Plan

Add a generic rule to `byN8NType`: when a type ends in `Tool` and its base is mapped and has a KilasFlow tool variant, translate with the base translator and carry `toolDescription`.

# Notes

Related tickets: BUG-6jvcs5

Related (from the audit): BUG-6jvcs5 (done). Its unchecked item "n8n's current tool variants (httpRequestTool, postgresTool...) are not mapped" still reproduces.

# Related Files

`results-ai.json` (HTTP Request Tool base, Gmail Tool, Google Sheets Tool). `internal/interop/n8n/n8n.go` has no `…Tool` entries apart from dataTableTool.

# Progress

- 2026-10-08: `byN8NType` falls back to `toolVariantMapping`: an n8n `<base>Tool` whose base node is mapped, and whose base KilasFlow type has an entry in `toolVariants`, imports with that tool's translator (the base node's translator plus `toolName`/`toolDescription`). The mapping is import-only, so the KilasFlow tool still exports through its own entry. `toolVariants` holds one entry today: `kilasflow.httpRequest` -> `kilasflow.httpTool` (`httpToolToKilas`, which is `httpToKilas` plus the tool fields). httpRequestTool therefore imports, carries `toolDescription`, and compiles on an agent.
- A `<base>Tool` whose base node imports but has no KilasFlow tool form stays a blocking placeholder, but the diagnostic now says so ("KilasFlow imports the n8n node \"n8n-nodes-base.gmail\", but not its agent-tool form ...: there is no kilasflow.gmail tool here yet") instead of "no equivalent".
- Tests: `internal/interop/n8n/tool_variant_import_test.go` (a 6270-style agent with an httpRequestTool imports with no blocking issue and compiles; gmailTool placeholder names the missing tool form).

## Remaining

- gmailTool, postgresTool and telegramTool still import as placeholders: KilasFlow has no Gmail, Postgres or Telegram tool node. Each needs a tool node in `nodes/` (derived with `toolVariantOf`) plus an agent tool kind that runs the base executor with `$fromAI` arguments, the way `httpToolFrom` runs the HTTP executor. Once a tool node exists, its import is one line in `toolVariants`.

# Attachments
