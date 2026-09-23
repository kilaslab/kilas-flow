---
id: FEAT-0hdfzd
title: 'MCP tool quality: inline documents, annotations, enums, real descriptions; record `--skills-used` beyond revisions'
status: todo
priority: medium
labels:
    - mcp
    - agents
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T03:06:00Z"
---

# Description

MCP tools only accept file paths for documents, carry no destructive/read-only annotations, and describe themselves generically. `--skills-used` is accepted everywhere but only recorded on workflow revisions.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-11, CLI-15). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## CLI-11: MCP tool schemas are thin: file-path-only documents, no annotations, generic descriptions, and tools that can't work or mutate local state

*gap · medium · mcp*

**n8n:** n8n's MCP server takes workflow code or JSON inline (`create_workflow_from_code`, `update_workflow`), and its tools carry read-only and destructive hints.

**Steps to reproduce:**

`tools/list` (61 tools), then read cs/mcp-tools.json. Call `workflow_create {"file":"-"}`, `auth_login {}`.

**Actual:**

- `workflow_create`, `workflow_validate`, `credential_create` and `credential_update` accept only `file`, a path on the MCP server's filesystem, and `-` is refused ("standard input is the MCP transport here"). A client that can't write to that disk cannot create anything. The `file` description still says "a path, or - for stdin".
- No tool has `annotations` (`readOnlyHint`/`destructiveHint`/`idempotentHint`), so a client can't auto-approve reads or flag the 14 guarded tools and `api`.
- Argument descriptions are circular: "the verb's workflow id, as `kilasflow workflow activate` takes it". Tool descriptions carry CLI syntax (`<datastoreId> <name> --type <type>`).
- Closed sets are free strings: column `type`, exec `status`, export `format`, skills `target` and `scope`.
- `auth_login` has no properties and always fails. `auth_logout` and `skills_install` (with `force`) change local files without `confirm`.
- Names mix separators: `workflow_get-version`, `workflow_publish-events`.
- The docs example `mcp serve --token "$KILASFLOW_TOKEN"` puts the token in the process listing, which the CLI's own hygiene rules forbid.

**Expected:**

An inline `document` (object) property next to `file`, annotations derived from `Guarded` and HTTP method, enum schemas, real descriptions, no `auth_login` tool (or one that reads the environment), and `confirm` on local mutators.

**Suggested fix:**

Generate `annotations` from the verb metadata. Add `document` for `--file` verbs by writing a temp file in the adapter. Emit `enum` from flag metadata.

**Evidence:**

cs/mcp-tools.json, cs/mcp2.out, internal/cli/mcp.go:349-420.

**Related:**

FEAT-qf0hsa (HTTP transport for `mcp serve`, not repeated here)


## CLI-15: `--skills-used` is accepted everywhere but recorded only on workflow revisions, and unknown names are kept

*gap · low · agent-skills / audit*

**n8n:** n8n's pack records `skillsUsed` on the tool call.

**Steps to reproduce:**

1. `workflow create --file wf.json --skills-used kilasflow-workflow-lifecycle,made-up-skill` → revision `actorMeta: ["kilasflow-workflow-lifecycle","made-up-skill"]`. 2. `workflow activate <wf> --yes --skills-used kilasflow-triggers` → `workflow publish-events` has no trace of it. 3. `datastore create … --skills-used x`.

**Actual:**

The flag appears on all 63 verbs, reads included. It is persisted only by `create`/`update` of workflow revisions; activation, credential and datastore writes drop it silently. Names aren't checked against the embedded bundle. The router says the flag doesn't exist (CLI-3), so nothing tells an agent to pass it. `workflow publish-events` also breaks the documented `{items,count,nextCursor}` normalisation (bare array; `--quiet` prints nothing).

**Expected:**

Record it on publish events and audit rows too, warn on names not in the bundle, register it only on mutating verbs, and normalise publish-events.

**Suggested fix:**

Carry the header through activation and the credential and datastore handlers. Validate names against `skills.Load()`.

**Evidence:**

transcript (authsrv `--skills-used` section).

**Related:**

none


# Acceptance Criteria
- [ ] The document tools accept an inline `document` object next to `file`
- [ ] Annotations (readOnly/destructive/idempotent) are derived from `Guarded` and the HTTP method; enums in schemas; real descriptions
- [ ] There is no `auth_login` tool that writes a token file, or it reads the token from the environment
- [ ] `--skills-used` is recorded on publish events and audit rows, warns on unknown names, and is registered only on mutating verbs

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-qf0hsa

# Related Files

# Attachments
