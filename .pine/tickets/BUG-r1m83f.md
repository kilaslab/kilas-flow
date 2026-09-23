---
id: BUG-r1m83f
title: Guarded operations bypass confirmation through `kilasflow api` and the MCP `api` tool
status: done
priority: high
labels:
    - cli
    - mcp
    - safety
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T04:40:46Z"
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
- [x] `api` resolves the operation id and asks for `--yes`/`confirm` for every operation that a guarded verb wraps
- [x] The MCP `api` tool is annotated destructive and requires `confirm` for those operations
- [x] A test enumerates the guarded operation ids and asserts that each is refused without confirmation through `api`

# Implementation Plan

Build `operationId → Verb.Guarded/Refusal` from the registry and call `requireConfirmation` and `requireAuthority` in `runAPI`. Accept `confirm` on the MCP `api` tool.

## Progress

`runAPI` (internal/cli/verbs_api.go) now builds `guardedByOperation()` from `registry()` and, for every non-`--list` call, looks up `args[0]` before the OpenAPI document is read. A match adapts the wrapped verb's metadata into a synthetic guard verb (`apiGuardVerb`, path = the operation id, e.g. `activate-workflow is \`workflow activate\`; pass --yes to confirm`) and runs it through the existing `requireConfirmation` then `requireAuthority` — the same two gates a named verb goes through, in the same order, so a refusal sends nothing and `--yes` on a scoped token still gets `scope_denied`. `--list` stays unguarded.

MCP (`internal/cli/mcp.go`): the `api` tool now publishes `confirm` in its schema (previously gated on `verb.Guarded`, which `api` never is) and its `descriptor()` carries `annotations.destructiveHint: true`, alongside every guarded tool. `mcp.Tool` (internal/mcp/server.go) gained `Annotations *ToolAnnotations` with `title`/`readOnlyHint`/`destructiveHint`/`idempotentHint`/`openWorldHint`, per the MCP 2026-07-28 schema, omitted when nil.

Docs: docs/src/content/docs/reference/cli.md — the escape hatch section (~187) now says the id-level guard applies before the "naming bypass" sentence; the MCP tools section (~675) documents `api`'s `confirm` and its always-on `destructiveHint`; the Guardrails section closes with a paragraph pointing at the escape hatch. `make generate-skills-command-reference-check` confirms no drift (the change is schema/annotation-only, not a `help --json` change).

Tests (all in internal/cli):
- `TestAPIRefusesGuardedOperationsWithoutConfirmation` (verbs_api_test.go) — loops `guardedInvocations(t)`, drives `api <op>` with a `countingTransport`, asserts `confirmation_required` and zero requests.
- `TestAPIRefusesAScopedTokenForAGuardedOperationEvenWithYes` — `--yes` with a scoped identity still gets `scope_denied`, only the identity read is sent.
- `TestAPIConfirmedGuardedOperationReachesTheStub` — loops every guarded operation id with `--yes` and a tenant-wide key, asserts the operation itself is reached.
- `TestMCPAPIToolRequiresConfirmationForAGuardedOperation` (mcp_test.go) — the same shape over a real `mcp serve` session (`operation_id: "activate-workflow"` without/with `confirm`).
- `checkMCPTool`/`checkMCPToolArgv` (mcp_test.go) relaxed and extended: `confirm` and `annotations.destructiveHint` are now expected on the escape hatch too, and `checkMCPToolArgv` exercises `api`'s `confirm` → `--yes` mapping through the real `FlagSet`, the same way it already does for guarded verbs.
- `TestAPIEscapeHatchWalksTheContract` (openapi_contract_test.go) updated to pass `--yes` on every row, since it now correctly reaches guarded operations only when confirmed.

Full suite: `go build ./...`, `go vet ./...`, `go test ./...` all green.

# Notes

Related (from the audit): none

# Related Files

cs/mcp2.out (the two `api` rows after the refused `workflow_deactivate`), cs/cmdlog.txt, internal/cli/verbs_api.go (guard lookup added), internal/cli/mcp.go, internal/mcp/server.go, docs/src/content/docs/reference/cli.md:187-189.

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23. Rewritten on 2026-09-23 to list only this ticket's own commits (`git log --grep "BUG-r1m83f" 2f8e9b1..5830b4e`) and the files exactly those commits changed.

- Range: `2f8e9b1..5830b4e`
- Commits (2):
  - `41b0f8b7` — chore(pine): close BUG-r1m83f with its landing evidence
  - `908dc479` — BUG-r1m83f: a guarded operation asks for confirmation through `api` too, by name or by id
- Files changed by those commits (merge commits excluded):

```
 .pine/tickets/BUG-r1m83f.md            | 311 ++++++++++++++++++++++++++++++++-
 docs/src/content/docs/reference/cli.md |  28 +++-
 internal/cli/mcp.go                    |  39 ++++-
 internal/cli/mcp_test.go               |  91 +++++++++-
 internal/cli/openapi_contract_test.go  |   8 +-
 internal/cli/verbs_api.go              |  59 ++++++-
 internal/cli/verbs_api_test.go         | 114 ++++++++++++
 internal/mcp/server.go                 |  32 +++-
 8 files changed, 653 insertions(+), 29 deletions(-)
```
