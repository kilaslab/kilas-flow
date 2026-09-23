---
id: BUG-r1m83f
title: Guarded operations bypass confirmation through `kilasflow api` and the MCP `api` tool
status: todo
priority: high
labels:
    - cli
    - mcp
    - safety
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T03:06:00Z"
---

# Description

The `--yes`/`confirm` guard applies only to named verbs. Through `kilasflow api` an agent can activate, delete, write credentials and delete tenants without confirmation. This reproduces on an auth-on server too.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-4). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n/a (n8n's MCP has no guard concept; KilasFlow advertises one).

# Steps to Reproduce

1. MCP: `workflow_deactivate {"workflow_id":W}` → `confirmation_required`, as designed. 2. Same session: `api {"operation_id":"deactivate-workflow","path":["id=W"]}` → ok. `api {"operation_id":"activate-workflow",…}` → ok, the endpoint is published. 3. CLI: `kilasflow api delete-workflow --path id=W` → deleted, no `--yes`. The same holds on the auth-on server with a tenant-wide key (cs/mcp3.py: the `api activate-workflow` row succeeds without `confirm`).

# Expected

`api` looks up whether the resolved operation id belongs to a guarded verb and asks for `--yes` / `confirm` the same way. MCP marks `api` as destructive (see CLI-11).

# Actual

The consent gate applies only to verb names. Every guarded operation (`activate-workflow`, `delete-workflow`, `create/update/delete-credential`, `create/delete/clear-datastore`, the column changes, `delete-tenant`) runs unconfirmed through the escape hatch. The MCP server exposes that hatch as one always-available tool. The docs call it "a naming bypass, never an authority bypass", but it is a consent bypass, and the stale skills (CLI-3) tell agents to use exactly this path.

# Acceptance Criteria
- [ ] `api` resolves the operation id and asks for `--yes`/`confirm` for every operation that a guarded verb wraps
- [ ] The MCP `api` tool is annotated destructive and requires `confirm` for those operations
- [ ] A test enumerates the guarded operation ids and asserts that each is refused without confirmation through `api`

# Implementation Plan

Build `operationId → Verb.Guarded/Refusal` from the registry and call `requireConfirmation` and `requireAuthority` in `runAPI`. Accept `confirm` on the MCP `api` tool.

# Notes

Related (from the audit): none

# Related Files

cs/mcp2.out (the two `api` rows after the refused `workflow_deactivate`), cs/cmdlog.txt, internal/cli/verbs_api.go (no guard lookup), docs/src/content/docs/reference/cli.md:187-189.

# Attachments
