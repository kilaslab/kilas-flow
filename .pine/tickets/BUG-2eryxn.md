---
id: BUG-2eryxn
title: MCP adapter breaks every boolean tool argument (`run.wait`, `api.list`, `skills_install.dry_run`)
status: doing
priority: high
labels:
    - mcp
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T04:07:22Z"
---

# Description

The adapter passes `--wait true` where Go's flag package needs `--wait=true`, so any boolean argument, even `false`, makes the call fail.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n/a

# Steps to Reproduce

1. `kilasflow mcp serve --url http://127.0.0.1:18080`, then initialize. 2. `tools/call run {"workflow_id":"<wf>","wait":true}`. 3. `tools/call api {"list":true}`. 4. `tools/call skills_install {"dry_run":true,"target":"dir:/tmp/x"}`. 5. The same with `false`.

# Expected

Booleans map onto the flag. `confirm` works only because it is special-cased to `--yes`.

# Actual

`run`: `{"code":"usage","message":"one workflow id at a time; got \"wf_…\" and \"true\""}`. `api`: `"--list enumerates every operation; it takes no operation id (got \"true\")"`. `skills_install`: `"skills install takes no arguments; got \"true\""`. Even `wait:false` fails. So an MCP agent can't wait for a run (it gets the 202 only), can't list operations, and can't dry-run an install.

# Acceptance Criteria
- [x] Boolean arguments map to `--flag=true|false` (or the bare flag)
- [x] A table-driven test covers every boolean flag the MCP tool schemas expose

# Implementation Plan

Emit `--name=true|false` for `mcpFlagBool`. Add a round-trip test that calls every tool with each boolean property set.

# Notes

Related (from the audit): none

# Related Files

cs/mcp4.out, cs/mcp3.py output (the `run` rows). Code: internal/cli/mcp.go:672-677 emits `[]string{"--"+name, "true"}`, but Go's `flag` needs `--wait=true` for bool flags, so `true` becomes a positional argument. mcp_test.go:422 only covers `wait` as an *unknown* argument.

# Attachments

## Progress

`mcpFlag.argv`'s `mcpFlagBool` case (`internal/cli/mcp.go`) now emits one
`--name=true|false` token instead of two separate `--name` and `true` tokens,
which is what let Go's `flag` package treat the value as a stray positional.

`checkMCPTool` (`internal/cli/mcp_test.go`, run per-tool by
`TestMCPToolsAreTheCommandTree`) no longer computes its expected argv by
calling `mcpFlag.argv` a second time — that comparison was circular, since a
bug in `argv` corrupted both sides of it identically, which is how this bug
shipped. It now feeds the generated argv through the real `FlagSet`
(`registerGlobalFlags` + `verb.Flags`, parsed with `parseFlags`, the same path
`cli.go` uses) via the new `checkMCPToolArgv` helper, called once with every
boolean sample `true` and once with every boolean sample `false`, and asserts
the positionals and every boolean flag's parsed value. This is the
table-driven coverage: every tool with a boolean flag (`run.wait`, `api.list`,
`skills_install.dry_run`, `skills_install.force`) is exercised as a subtest.

`TestMCPServeAnswersARealClient` gained a live round trip over real JSON-RPC
frames and a real stub HTTP server: `run {"workflow_id":"wf_1","wait":false}`,
`api {"list":true}` and `skills_install {"dry_run":true,"target":"dir:<tmp>"}`
each now succeed end to end, reproducing the ticket's exact repro steps.

Verified RED before the fix: both `TestMCPToolsAreTheCommandTree/run`,
`/api`, `/skills_install` and `TestMCPServeAnswersARealClient` failed with the
ticket's own symptom (e.g. `one workflow id at a time; got "wf_1" and
"false"`). GREEN after the one-line fix; `go test ./internal/cli/` and
`go vet ./internal/cli/` are clean.
