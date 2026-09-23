---
id: FEAT-nq1vsx
title: 'AI tools: Think, Wikipedia, SerpAPI, Code Tool, and an MCP Server Trigger'
status: todo
priority: medium
labels:
    - n8n
    - parity
    - ai
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
---

# Description

n8n's most-viewed template (1954, 780K views) uses the SerpAPI tool.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-15). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** SerpAPI appears in 11 templates, Wikipedia in 10, Code Tool in 9, Think Tool in 8, MCP Server Trigger in 8 and Wolfram Alpha in 0. The cluster appears in 4.5% of templates. The canonical n8n "AI agent chat" template (1954, 780K views, the most-viewed template) uses SerpAPI.

# Steps to Reproduce

Import Manual → AI Agent ← OpenAI model + tool X, using real instances: toolThink 2800, toolCode 19000, toolWikipedia 19217, toolSerpApi 1954, plus a docs-derived Wolfram tool. Import MCP Server Trigger 3638 → No Op.

# Expected

Think (a no-op scratchpad tool), Wikipedia and SerpAPI (plain REST) import and run. Code Tool follows NG-1. MCP Server Trigger exposes workflow tools over MCP; KilasFlow already has an MCP server for CLI verbs.

# Actual

Every one is a blocking placeholder. SerpAPI is the sole blocker in 2 of the most-viewed templates.

# Acceptance Criteria
- [ ] Think Tool (a no-op scratchpad)
- [ ] Wikipedia and SerpAPI tools, with a `serpApi` credential
- [ ] Code Tool, once the JS runtime exists
- [ ] An MCP Server Trigger that publishes a workflow's attached tools at a per-workflow MCP route

# Implementation Plan

Think Tool is trivial. Wikipedia and SerpAPI are small REST tools with a `serpApi` credential. An MCP Server Trigger can reuse the `mcp serve` plumbing, publishing attached tools at a per-workflow route.

# Notes

Related (from the audit): none

# Related Files

`results-ai.json` (Think Tool, Code Tool, Wikipedia Tool, SerpAPI Tool, Wolfram Alpha Tool), `results-extra.json` (MCP Server Trigger)

# Attachments
