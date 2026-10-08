---
id: BUG-2n4rfz
title: /docs shows Scalar 'Ask AI', 'Generate MCP' and registry search blocked by the page CSP (4 console errors)
status: done
priority: low
labels:
    - docs
    - csp
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

The self-hosted API reference still offers a third-party upload feature that can't work under the page's CSP, and would leak the document if it could.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-21). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n/a

# Steps to Reproduce

1. Open /docs. 2. Read the console. 3. Click "Ask AI".

# Expected

A self-hosted reference with no third-party AI or upload affordances and no console errors.

# Actual

The console shows 4 errors: "Connecting to 'https://api.scalar.com/vector/registry/curated' violates … connect-src 'self'". "Ask AI" opens a chat that says "By messaging Agent Scalar your OpenAPI document will be temporarily uploaded to Scalar's servers" and can't work here. "Generate MCP" links out to Scalar. /docs is also always light while the app is dark.

# Acceptance Criteria
- [x] The Scalar configuration hides the agent, MCP and registry features
- [x] There are no console errors on /docs
- [x] /docs follows the app's dark theme

# Implementation Plan

Extend the Scalar configuration to hide the AI agent, MCP and registry features (for example agent/mcp disabled, hideClientButton) and set darkMode:true.

# Notes

Related (from the audit): none

# Fix (2026-10-08)

`internal/api/docs.go` `docsConfiguration` now also sets `agent.disabled`, `mcp.disabled`, `showDeveloperTools: "never"` and `darkMode: true` (option names confirmed in the vendored @scalar/api-reference 1.67.0 bundle and Scalar's configuration docs). Agent Scalar was also what fetched `api.scalar.com/vector/registry/curated`. The developer toolbar that Scalar shows on localhost carried Share and Deploy menus that publish the document to Scalar, so it is off too.

- Test: `TestDocsUIOffersNoThirdPartyFeatures` (internal/api/server_test.go) parses the page's configuration and checks all four settings. It failed before the change.
- Live self-test with playwright-cli against the built binary (`make build-web`, port 18990): the console on /docs has 0 errors and 0 warnings (only Scalar's version info line), `body.dark-mode` is set, and no Ask AI, Generate MCP, Developer Tools, Share or Deploy control is on the page.

# Related Files

agents/ux-ops/43-docs.png, agents/ux-ops/44-docs-ask-ai.png; internal/api/docs.go:80 `docsConfiguration = {"withDefaultFonts":false,"hideModels":false}`. The comment at :84-85 already acknowledges the api.scalar.com fetches.

# Attachments
