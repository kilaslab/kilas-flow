---
id: BUG-y38bss
title: Every guarded CLI verb and MCP confirm:true call fails with 401 when auth is off (the default)
status: done
priority: high
labels:
    - cli
    - mcp
    - auth
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T04:23:12Z"
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
- [x] With auth off, the authority pre-check passes, and a guarded verb runs once `--yes` (or MCP `confirm: true`) is given
- [x] With auth on, scoped-token checks are unchanged
- [x] A test runs activate/delete/credential create against an auth-off server through both the CLI and MCP

# Progress

`requireAuthority` (internal/cli/cli.go) no longer takes a 401 from `/auth/me` at its word: a 401 is ambiguous by itself (auth-off has no principal to describe and answers 401 for every credential, but auth-on answers the same way to a bad credential), so a first pass that returned `nil` on any "unauthenticated" 401 would fail the gate open for a scoped agent token whose credential happened to be wrong. An automated security review caught this during implementation; the fix now confirms "auth is off" against the server's OpenAPI document rather than inferring it.

- `Client.AuthEnabled` (internal/cli/openapi.go) reads whether the document declares a root `security` requirement — server.go attaches one only when `Auth.Enabled` is true, pinned by `TestOpenAPIDeclaresNoRequirementWhenAuthIsDisabled` / `TestOpenAPIDeclaresBothCredentialsWhenAuthIsEnabled` in internal/api/openapi_security_test.go. It shares `Client.Operations`'s fetch and per-invocation cache (`indexOperations` now also returns the flag), so confirming this costs one extra GET to the already-public `/api/openapi.json`, only on the 401 path.
- `requireAuthority`: whoami 401 -> call `AuthEnabled`; no requirement and no read error -> let the operation through (`nil`); a requirement, or a document that could not be read, -> the original 401 stands (fail closed). Any other whoami error is unchanged. A valid identity's scoped-token refusal is untouched.

Tests (internal/cli):
- `TestGuardedVerbWithAuthOffReachesTheServer` — every `guardedInvocations` row, `/auth/me` 401 + an OpenAPI document with no root security, reaches the server with `--yes`.
- `TestGuardedVerbKeepsThe401WhenAuthIsOn` — same 401, but the document declares a root security requirement: refused with the original `unauthenticated` 401, nothing beyond the identity and document reads is sent.
- `TestGuardedVerbKeepsThe401WhenTheDocumentCannotBeConfirmed` — the document read itself fails (500): refused the same way (fail closed).
- `TestMCPGuardedToolRunsWithAuthOff` — the MCP `confirm: true` path through the same auth-off shape.
- `TestGuardedVerbRefusesAScopedTokenEvenWithYes`, `TestGuardedVerbWithATenantWideKeyReachesTheServer`, `TestMCPGuardedToolsRequireConfirmation` (existing, auth-on scoped-token paths) pass unchanged.

`go build ./...`, `go vet ./...`, `go test ./internal/cli/... -count=1`, and `go test ./internal/api/... -run TestOpenAPI` all pass.

# Implementation Plan

In `requireAuthority`, treat a 401 from /auth/me as "not scoped" and let the operation's own status decide. Better, read an `authEnabled` capability once BUG-719gaz adds one. Add a test against an auth-off server.

# Notes

Related tickets: BUG-719gaz

Related (from the audit): BUG-719gaz (the same missing auth-enabled signal makes /auth/me 401 with auth off). That ticket covers the UI, not this CLI and MCP blocker.

# Related Files

cs/cmdlog.txt (task a/b/c/f rows with rc=3). The `--verbose` trace is in the transcript. MCP result is in cs/mcp2.out. Code: internal/cli/cli.go:256-276 (`requireAuthority` returns the `whoamiWith` error as-is).

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23. Rewritten on 2026-09-23 to list only this ticket's own commits (`git log --grep "BUG-y38bss" 2f8e9b1..5830b4e`) and the files exactly those commits changed.

- Range: `2f8e9b1..5830b4e`
- Commits (2):
  - `ff39847f` — chore(pine): close BUG-y38bss with its landing evidence
  - `de7781c2` — BUG-y38bss: a guarded verb runs when auth is off, confirmed by the server's own OpenAPI document
- Files changed by those commits (merge commits excluded):

```
 .pine/tickets/BUG-y38bss.md | 302 +++++++++++++++++++++++++++++++++++++++++++-
 internal/cli/cli.go         |  22 ++++
 internal/cli/client.go      |   5 +
 internal/cli/guard_test.go  | 144 +++++++++++++++++++++-
 internal/cli/mcp_test.go    |  34 +++++
 internal/cli/openapi.go     |  38 +++++--
 6 files changed, 526 insertions(+), 19 deletions(-)
```
