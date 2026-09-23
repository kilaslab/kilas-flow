---
id: FEAT-53pa9a
title: Structured server log line for every terminal execution (failed runs leave no trace today)
status: todo
priority: medium
labels:
    - observability
    - ops
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

Operators alert on logs. Today a failed execution leaves no log line at all.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-22). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Execution failures reach the instance log and log streaming (workflow failed events with execution id, workflow and node), which is what operators alert on.

# Steps to Reproduce

1. Fail several executions (a, b, c, e above). 2. `grep -v 'msg="http request"' $SP/kf-server.log | grep "^time="`.

# Expected

One structured line per terminal execution (`execution finished status=failed execution=… workflow=… node=… error=…`) at WARN for failures.

# Actual

Apart from startup lines, the only non-request entries in 61k lines are `error workflow started` and `error workflow failed`. A failed execution leaves no log line; `exec_01a0cbc5-4638-…` appears only in GET request paths.

# Acceptance Criteria
- [ ] One slog line per terminal execution with status, execution id, workflow id and name, the failing node, and the error; WARN for failed runs
- [ ] Its level can be configured, and a test asserts the line is emitted on failure

# Implementation Plan

Log in the engine's terminal path (`fail`/`complete`) with slog fields, controlled by level.

# Notes

Related tickets: BUG-y57cz4

Related (from the audit): BUG-y57cz4 (done, observability)

# Related Files

`$SP/kf-server.log`.

# Attachments
