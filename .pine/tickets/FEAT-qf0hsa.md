---
id: FEAT-qf0hsa
title: 'MCP: SSE transport (or a clear error) for the MCP Client Tool; an HTTP transport for `kilasflow mcp serve`'
status: todo
priority: low
labels:
    - ai
    - mcp
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-09-23T01:56:13Z"
---

# Description

An SSE MCP endpoint fails with an HTML 404, and KilasFlow's own MCP server is stdio-only, so an agent inside KilasFlow cannot attach it.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama; finding ids: AI-16). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The MCP Client Tool supports the HTTP Streamable and SSE transports and has include-all / selected / all-except modes.

# Steps to Reproduce

1. `PORT=18914 npx -y @modelcontextprotocol/server-everything sse`.
2. mcpClientTool `serverUrl: http://127.0.0.1:18914/sse`.
3. Run.

# Expected

SSE transport support, or an error saying "this looks like an SSE endpoint; KilasFlow needs the streamable HTTP /mcp endpoint". An exclude list. An HTTP transport for `kilasflow mcp serve`.

# Actual

`MCP initialize failed with status 404: <!DOCTYPE html>…<pre>Cannot POST /sse</pre>…`, with no hint that SSE is unsupported. Streamable HTTP works (`get-sum` → `echo`, correct). `kilasflow mcp serve` is stdio-only, and the client refuses `stdio`, so a KilasFlow agent cannot use KilasFlow's own MCP server. Importing n8n `toolsToInclude: allExcept` exposes *every* tool, including the ones the author excluded (reported only as lossy).

# Acceptance Criteria
- [ ] The MCP Client Tool supports SSE, or says "this looks like an SSE endpoint; use the streamable HTTP /mcp endpoint"
- [ ] A tool include/exclude list
- [ ] `kilasflow mcp serve --http` (authenticated)

# Implementation Plan

Detect `text/event-stream` on GET or a 404/405 on POST and name SSE in the error. Add an `excludeTools` parameter. Treat the imported `allExcept` as an exclude list.

# Notes

Related (from the audit): none

# Related Files

`case-6-sse.execution.json`, `case-6-all.execution.json`. Code `nodes/ai.go:3136-3160`, `internal/interop/n8n/parameters.go:4850-4858`.

# Attachments
