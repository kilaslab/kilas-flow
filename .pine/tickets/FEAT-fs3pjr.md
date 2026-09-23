---
id: FEAT-fs3pjr
title: 'CLI ergonomics: bare/n8n node names in `node describe`, human output with data and failure reasons, real `--help`, actionable errors, richer `context`'
status: todo
priority: medium
labels:
    - cli
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T03:06:00Z"
---

# Description

The JSON envelope is solid. The human-facing layer around it is thin: `datastore rows` drops every data column, `run --wait` never shows why a run failed, and `--help` is a bare flag dump.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-6, CLI-8, CLI-10, CLI-14, CLI-13). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## CLI-6: `node describe` needs the `kilasflow.` prefix, the reference's own example fails, and n8n type names aren't resolved

*ux · medium · cli / node-catalog*

**n8n:** n8n tooling accepts `n8n-nodes-base.webhook` and the short name `webhook`.

**Steps to reproduce:**

`kilasflow node describe webhook`, `… httpRequest`, `… n8n-nodes-base.webhook`, `… kilasflow.webhok`.

**Actual:**

The first three exit 4 with `no node type "webhook" in the catalogue` and no suggestion. Only the typo within 2 edits gets `did you mean kilasflow.webhook?`. docs/…/reference/cli.md:424 documents `kilasflow node describe httpRequest # one entry, newest version`, which fails.

**Expected:**

Accept the bare name, map n8n types through the importer's `SupportedMappings`, and fall back to substring matches in the hint.

**Suggested fix:**

Normalise `want` by prefixing `kilasflow.` when there's no dot, look up the n8n mapping table, and suggest substring and suffix matches.

**Evidence:**

cs/cmdlog.txt 09:07:01-07. internal/cli/verbs_node.go:209-240 (`closeTypes` uses an edit distance of at most 2 on the full name).

**Related:**

none


## CLI-8: The human output hides the data and the failure reason

*ux · medium · cli-output*

**n8n:** `n8n execute --id` prints the node error. The executions list shows the error message.

**Steps to reproduce:**

On a terminal (I used `script -q /dev/null …`): 1. `kilasflow datastore rows <ds>`. 2. `kilasflow run <failingWf> --wait`. 3. `kilasflow exec get <failedExec>`. 4. `kilasflow exec trace <failedExec>`. 5. `kilasflow node describe kilasflow.set`, `workflow diagnostics <wf>`.

**Actual:**

1. `rows` prints only `id createdAt updatedAt`; the data columns (sku, qty, note) are dropped, and no `nextCursor` is shown.
2. `run --wait` prints only `kilasflow: execution exec_… failed`. The JSON `error.message` is the same, with no node and no reason.
3. `exec get` prints id, workflow, status, trigger and times, with `duration -`. It shows no error, no failing node and no node runs.
4. `trace` prints `node.failed node=call status=failed` without the message.
5. `describe` and `diagnostics` are pretty-printed JSON dumps.

**Expected:**

The human views show the row data, the failing node and its error message, and a node-run table with durations. `execution_failed` includes the first failed node and its message in `error.message`.

**Suggested fix:**

Render the rows with the datastore's columns. Add an error line and a node-run table to `humanExecution` and `humanTrace`. Build the `execution_failed` message from the record's `error.message`.

**Evidence:**

cs/human.sh output in the transcript. internal/cli/verbs_datastore.go:59 (`humanResourceList("id","createdAt","updatedAt")`), internal/cli/verbs_exec.go:392-440 (`humanExecution`).

**Related:**

none


## CLI-10: `--help` is a flag dump: no arguments, no examples, noun-only help fails, and unknown flags break the envelope

*ux · medium · cli-help*

**n8n:** `n8n --help` lists the commands, and each command's help has examples.

**Steps to reproduce:**

1. `kilasflow <verb> --help` for all 63 verbs. 2. `kilasflow workflow`, `kilasflow workflow --help`, `kilasflow help workflow`. 3. `kilasflow --help`. 4. `kilasflow workflow export x --out y --json`. 5. `kilasflow completion zsh`.

**Actual:**

1. Every verb prints `usage: kilasflow <verb> [flags] [args]` and then about 10 global flags. It doesn't name the positional arguments (`workflow get-version` takes two ids; you only learn that from the error), and there are no examples. `--yes` and `--skills-used` appear on read-only verbs. `run --help` says `-timeout … (default 30s)`, but `--wait` defaults to 5 minutes.
2. `kilasflow workflow` and `workflow --help` → `unknown command "workflow"`. `help workflow` ignores the noun and prints all 63.
3. `kilasflow --help` prints only the *server* flags (`-config`, `-role`, …) with no pointer to `kilasflow help`.
4. An unknown flag writes Go's usage to stderr and nothing to stdout, even with `--json` (exit 2), so an agent parsing stdout gets an empty document.
5. There's no shell completion and no man page.

**Expected:**

Usage lines with named arguments (`kilasflow workflow get <workflowId>`), one example per verb (they already exist in cli.md), per-noun help, a CLI hint in `kilasflow --help`, and a usage envelope for flag errors in JSON mode.

**Suggested fix:**

Add `Args`/`Example` to the Verb metadata and render them. Route `flag.ErrHelp` and parse errors through the envelope writer. Add `kilasflow help <noun>` and `completion bash|zsh|fish`.

**Evidence:**

cs/help/*.txt (all 63), the transcript.

**Related:**

BUG-b4cb1c (LEAD-10, the `--file` "workflow document" wording on credential verbs)


## CLI-14: Error messages stop short of the fix

*ux · low · cli-errors*

**Steps to reproduce:**

1. With a scoped `workflow:read,workflow:run` key, run `datastore list`. 2. With no token, run `workflow list` against the auth-on server. 3. `debug eval '$json.target' --execution <id> --node Prepare`. 4. `workflow get-version <wf> wfv_nope`. 5. `workflow list --url ''`. 6. A network failure.

**Actual:**

1. `"This agent token cannot read."`, which doesn't name the missing `datastore:read` scope.
2. `401 … needs an API key or a signed-in session`, with no hint to run `kilasflow auth login`.
3. `"the execution's trace has no such node: Prepare"`: it wants the node id, and the message lists no valid ids.
4. `"workflow not found"` for a missing *revision*.
5. Exit 1 `network_error … unsupported protocol scheme ""`, where cli.md:803-805 documents exit 2 naming the four ways to set a URL.
6. `dial tcp …: connection refused`, with no mention of `--url`/`KILASFLOW_URL`.

**Expected:**

Each message names the next action.

**Suggested fix:**

Map scope refusals to the required scope. Add CLI-side hints for 401 and network errors. List the node ids in the eval refusal. Handle the empty-URL case before building the client.

**Evidence:**

cs/cmdlog.txt, transcript (authsrv section).

**Related:**

BUG-j7qrp2 (debug eval semantics)


## CLI-13: `context` is a thin first-turn briefing

*ux · low · cli-context*

**n8n:** n/a (n8n's MCP `get_instance_info`-style tools)

**Steps to reproduce:**

`kilasflow context` against :18080 (auth off) and :18097 (scoped key).

**Actual:**

With auth off, identity reads `unauthenticated: this request is not authenticated`, which suggests a login is needed when none exists. `workflows.count` is 20, the page size, and `truncated:true`, not the total (100+). The identity omits `scopes`, though `auth whoami` shows them and an agent needs them to plan. There's no credentials count, no pointer to the skills bundle or its drift status, and the human view lists no names ("20 listed (more available)").

**Expected:**

`identity: {authDisabled:true}` or similar, a total or `approximate` count, `scopes`, `credentials.count`, and `skills: {installed, bundleVersion, drift}`.

**Suggested fix:**

Extend `contextPayload`, reusing `skills check` and `list-credentials?limit=1`.

**Evidence:**

cs/context.json, the transcript (human view and scoped `context`).

**Related:**

BUG-719gaz


# Acceptance Criteria
- [ ] `node describe httpRequest` and n8n type names resolve, with substring hints
- [ ] Human output shows row data, the failing node and its message, and a node-run table with durations; `execution_failed` carries the first failed node in `error.message`
- [ ] `--help` shows usage with named arguments, one example per verb and per-noun help; an unknown flag returns a usage envelope in JSON mode
- [ ] Every error message names the next action
- [ ] `context` reports whether auth is disabled, scopes, counts and skills drift

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-719gaz, BUG-b4cb1c, BUG-j7qrp2

# Related Files

# Attachments
