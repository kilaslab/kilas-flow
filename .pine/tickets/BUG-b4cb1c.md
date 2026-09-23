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
updated: "2026-09-23T01:34:44Z"
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
- [ ] FEAT-3taswf is reopened as blocked on the first release tag, or its unticked criteria move to a new ticket
- [ ] The node counts in the docs are re-measured (done in the audit change on 2026-09-23), and a drift gate compares the prose with GET /node-types
- [ ] `credential create --help` describes --file as a credential document; the shared flag help takes a per-verb noun
- [ ] The CLI reference examples run as written (checked in CI), and the catalogue size and exit-code claims are generated

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Related Files

# Attachments
