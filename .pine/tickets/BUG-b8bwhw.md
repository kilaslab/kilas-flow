---
id: BUG-b8bwhw
title: Importer does not map *Tool variants of native nodes (httpRequestTool, gmailTool, postgresTool)
status: todo
priority: medium
labels:
    - n8n
    - importer
    - ai
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
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
- [ ] A generic rule maps `<type>Tool` onto the KilasFlow tool variant whenever the base node is mapped, carrying `toolDescription`
- [ ] httpRequestTool, gmailTool, postgresTool and telegramTool import and run as agent tools
- [ ] Template 6270 imports with no blocking issue

# Implementation Plan

Add a generic rule to `byN8NType`: when a type ends in `Tool` and its base is mapped and has a KilasFlow tool variant, translate with the base translator and carry `toolDescription`.

# Notes

Related tickets: BUG-6jvcs5

Related (from the audit): BUG-6jvcs5 (done). Its unchecked item "n8n's current tool variants (httpRequestTool, postgresTool...) are not mapped" still reproduces.

# Related Files

`results-ai.json` (HTTP Request Tool base, Gmail Tool, Google Sheets Tool). `internal/interop/n8n/n8n.go` has no `…Tool` entries apart from dataTableTool.

# Attachments
