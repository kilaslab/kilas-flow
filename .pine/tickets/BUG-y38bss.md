---
id: BUG-y38bss
title: Every guarded CLI verb and MCP confirm:true call fails with 401 when auth is off (the default)
status: todo
priority: high
labels:
    - cli
    - mcp
    - auth
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T03:06:00Z"
---

# Description

In the default configuration, an agent cannot activate, delete, or create credentials or data tables through the CLI or MCP at all: the pre-check against `/auth/me` treats its 401 as fatal.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-1). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** `n8n update:workflow --active=true` and the public API work on any instance that has the API enabled; there's no extra identity probe.

# Steps to Reproduce

1. Start the server with auth off (the default; this is the shared :18080). 2. `kilasflow workflow activate <wf> --yes --url http://127.0.0.1:18080`. 3. Repeat with `datastore create x --yes`, `datastore columns add <ds> qty --type number --yes`, `credential create --file c.json --yes`, `workflow delete <wf> --yes`. 4. In MCP, call `workflow_deactivate {"workflow_id":…, "confirm": true}`.

# Expected

With auth off there is no scoped token to refuse, so the authority check should pass and the verb should run once `--yes` is given.

# Actual

Every one exits 3 with `{"code":"unauthenticated","message":"this request is not authenticated","status":401}`. `--verbose` shows the only request sent is `> GET /api/v1/auth/me` / `< 401`. The operation itself would have succeeded: `curl -X POST …/workflows/<id>/activate` → 200, and `kilasflow api activate-workflow` → ok.

# Acceptance Criteria
- [ ] With auth off, the authority pre-check passes, and a guarded verb runs once `--yes` (or MCP `confirm: true`) is given
- [ ] With auth on, scoped-token checks are unchanged
- [ ] A test runs activate/delete/credential create against an auth-off server through both the CLI and MCP

# Implementation Plan

In `requireAuthority`, treat a 401 from /auth/me as "not scoped" and let the operation's own status decide. Better, read an `authEnabled` capability once BUG-719gaz adds one. Add a test against an auth-off server.

# Notes

Related tickets: BUG-719gaz

Related (from the audit): BUG-719gaz (the same missing auth-enabled signal makes /auth/me 401 with auth off). That ticket covers the UI, not this CLI and MCP blocker.

# Related Files

cs/cmdlog.txt (task a/b/c/f rows with rc=3). The `--verbose` trace is in the transcript. MCP result is in cs/mcp2.out. Code: internal/cli/cli.go:256-276 (`requireAuthority` returns the `whoamiWith` error as-is).

# Attachments
