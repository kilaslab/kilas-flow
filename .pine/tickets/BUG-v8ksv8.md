---
id: BUG-v8ksv8
title: Unknown /api/* paths return 200 with the SPA HTML instead of a JSON 404
status: todo
priority: medium
labels:
    - api
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

A client, the CLI's `api` verb or an agent sees success, and then fails trying to parse HTML.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-18). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Unknown /rest and /api/v1 routes return 404 JSON.

# Steps to Reproduce

1. `curl -i http://127.0.0.1:18080/api/v1/nonexistent`. 2. `curl -i http://127.0.0.1:18080/api/v1/schedules/sched_x` (GET on a single schedule isn't implemented).

# Expected

`404 application/problem+json` for anything under /api/.

# Actual

Both return `200 text/html; charset=utf-8` with the SvelteKit index.html, and so does /api/v2/foo. A client, the CLI's `api` verb or an agent sees success and then fails parsing HTML.

# Acceptance Criteria
- [ ] Anything under `/api/` that doesn't match a route returns `404 application/problem+json`
- [ ] A test covers /api/v1/nonexistent and /api/v2/foo

# Implementation Plan

Exclude the /api/ prefix from the SPA fallback handler and return a problem+json 404.

# Notes

Related (from the audit): none

# Related Files

curl output in this report; the SPA fallback in internal/web.

# Attachments
