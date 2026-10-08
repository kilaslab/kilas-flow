---
id: BUG-b4cb1c
title: 'Board/doc drift: FEAT-3taswf ''done'' but @kilasflow/sdk not on npm; stale node counts; CLI help nouns'
status: todo
priority: medium
labels:
    - board-hygiene
    - docs
    - cli
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-10-08T16:30:00Z"
---

# Description

Small drifts between what the board or docs claim and what exists. Following the project's own rule ("A ticket marked done is not evidence the deliverable exists"), they are recorded here.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: lead; finding ids: LEAD-11, LEAD-12, LEAD-10). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## LEAD-11: FEAT-3taswf "Publish @kilasflow/sdk to npm" is done, but the package isn't on npm

*docs · medium · release / board hygiene*

**Steps to reproduce:**

`npm view @kilasflow/sdk version` returns E404

**Actual:**

The ticket is status done while its first and fifth acceptance criteria are unticked ("npm install resolves", "publishing is driven by a git tag"). EPIC-bkj6yf and FEAT-5fhj6p depend on it.

**Expected:**

Status blocked or doing until a tag publishes, per the project's own rule ("A ticket marked done is not evidence the deliverable exists").

**Suggested fix:**

Reopen as blocked on the first release tag.


## LEAD-12: Docs stated 49 node types / 56 pairs; the live catalogue has 61 / 68

*docs · low · docs*

**Actual:**

docs/src/content/docs/concepts/node-registry.md and start/what-kilasflow-is.md both said 49/56 and claimed to be "measured".

**Suggested fix:**

Fixed in this change (re-measured 2026-09-23). A drift gate like skills G1-G5 would stop it recurring.


## LEAD-10: `credential create --help` describes --file as a "workflow document"

*docs · low · cli*

**Actual:**

`-file string  workflow document to send: a path, or - for stdin` on `kilasflow credential create --help`

**Expected:**

"credential document to send"

**Suggested fix:**

Make the shared flag help text take the noun per verb.


# Also found by the audit

## CLI-18: CLI reference drift: catalogue size, a broken example, and exit-code claims

*docs · low · docs-cli*

**Steps to reproduce:**

Read docs/src/content/docs/reference/cli.md against the binary.

**Actual:**

Line 21 says "a 90-entry catalogue" (the live one has 68). Line 424, `kilasflow node describe httpRequest`, exits 4 (CLI-6). Lines 803-805 say `--url ''` gives exit 2; the binary gives exit 1 `network_error` (CLI-14). Line 650 passes `--token "$KILASFLOW_TOKEN"` on the command line (CLI-11). Line 116's "deliberately absent" list is right, but the skills contradict it (CLI-3).

**Expected:**

Examples run as written. Counts are generated.

**Suggested fix:**

Run the cli.md fenced examples in the docs gate against a test server.

**Evidence:**

the cli.md lines quoted, cs/cmdlog.txt.

**Related:**

BUG-b4cb1c (stale node counts in other pages)


# Acceptance Criteria
- [x] FEAT-3taswf is reopened as blocked on the first release tag, or its unticked criteria move to a new ticket
- [x] The node counts in the docs are re-measured (done in the audit change on 2026-09-23), and a drift gate compares the prose with GET /node-types
- [x] `credential create --help` describes --file as a credential document; the shared flag help takes a per-verb noun
- [ ] The CLI reference examples run as written (checked in CI), and the catalogue size and exit-code claims are generated

## Progress (2026-10-08)

Criteria 1-3 done; criterion 4 (CLI-18) only partly, so the ticket stays `todo`.

- **FEAT-3taswf** reopened: `status: todo` plus a `blocked` label (the board has no blocked column; `pine doctor` flags `status: blocked`), with a note at the top of the ticket that it waits on the first `sdk-v*` tag publishing through `release.yml`'s sdk job, and which criteria (1, 5, 7) to tick then. Nothing was published.
- **Node counts** re-measured: a default install now serves 62 types across 69 pairs (59 types / 64 pairs compiled, 3 / 5 from the Telegram and WAHA packs), one more than the 2026-09-23 audit's 61/68. `concepts/node-registry.md` and `start/what-kilasflow-is.md` corrected. Drift gate: `cmd/kilasflow/catalogue_docs_test.go` registers what `run()` registers before directory packs (`nodes.RegisterAll`, `nodes.RegisterVectorNodes`, `telegram.Register`, `waha.Register`), counts types and pairs the way `GET /node-types` lists them, and fails when either page's sentence disagrees. It caught the 61→62 drift on its first run. Limit: it mirrors `run()`'s registration calls rather than calling the endpoint, so a new embedded pack added to `run()` must be added there too.
- **CLI help nouns:** `registerCreateFlags(noun)` takes the verb's noun; `credential create/update -h` say "credential document to send", `workflow create -h` says "workflow document to send". Test: `internal/cli/document_flag_help_test.go`. `make generate-skills-command-reference-check` up to date.
- **CLI-18, partly:** cli.md no longer states a catalogue size ("90-entry" → "the whole catalogue"), and the `node describe` / `node options` examples use the full type `kilasflow.httpRequest` (describe matches the type exactly, so `httpRequest` exited 4).

Left for criterion 4: run cli.md's fenced examples against a test server in CI; and the skills' contradiction of the "deliberately absent" list (CLI-3).

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Related Files

# Attachments

## Fix (2026-10-08, CLI-14)

The binary now does what cli.md says. Every verb that sends a request with no base URL (`--url ''`) is refused with exit 2 and `no server URL: pass --url, set KILASFLOW_URL, or name one in the configuration file`. Before, it exited 1 with `network_error: unsupported protocol scheme ""`. The check sits in the CLI client's `buildRequest`, so it fires only where a request would have been sent, after each verb's own argument checks. Test: `TestAServerVerbWithNoServerURLIsAUsageError` (internal/cli/exit_test.go), which failed first for `workflow list`, `workflow get`, `exec get` and `credential list`. Still left for criterion 4: the CI job that runs cli.md's examples, CLI-11 and CLI-3.

CLI-11 (2026-10-08): the `mcp serve` example in cli.md no longer passes `--token "$KILASFLOW_TOKEN"` on the command line, where a process listing shows it. It relies on `KILASFLOW_TOKEN` from the environment, which the verb already reads.

Criterion 4, partly (2026-10-08): `TestEveryVerbCliMdRunsExists` (internal/cli/docs_examples_test.go) checks, without a server, that every `kilasflow …` line in cli.md's code blocks names a verb of the live command tree (up to three words, e.g. `datastore columns add`). It passes today, and a planted `kilasflow apix` made it fail. Still open: running the examples themselves against a test server in CI, and CLI-3.

