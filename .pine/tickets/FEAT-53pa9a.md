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
- [x] One slog line per terminal execution with status, execution id, workflow id and name, the failing node, and the error; WARN for failed runs
  - Evidence: `Service.logTerminal` in `internal/engine/terminal_log.go` writes `execution finished` with `status`, `execution`, `workflow`, `workflowName`, `tenant`, `trigger`, `node` (failed runs), `errorCode`, `error`, `durationMs` and `parentExecution`; WARN when failed, INFO otherwise. Pinned by `TestAFailedExecutionWritesOneWarnLineNamingTheFailingNode`, `TestASucceededExecutionWritesOneInfoLine` and `TestACancelledQueuedExecutionWritesAnInfoLine` in `internal/engine/terminal_log_test.go`.
  - Secrets and item data: the error text is the one persisted on the record, which the runner already scrubbed of every secret the failing node resolved (`resolvedSecrets.scrub`, `internal/engine/secret_scrub.go`); the line carries no input, output or item. `TestTheTerminalLineCarriesNeitherASecretNorItemData` fails a node with the resolved credential value in its error and plants a marker in the trigger payload, then asserts neither reaches the log and that the logged error equals the persisted one (`...?api_key=[redacted]`).
- [x] Its level can be configured, and a test asserts the line is emitted on failure
  - Evidence: new `log.execution_level` (`KILASFLOW_LOG_EXECUTION_LEVEL`, default `info`; `info` = every terminal run, `warn` = failed runs only, `off` = none; anything else is refused at startup by `Config.Validate`), wired in `cmd/kilasflow/main.go` to `engine.ServiceDeps.ExecutionLogLevel`. `TestTheTerminalLineLevelIsConfigurable` runs one failed and one succeeded execution at each level; `TestTheExecutionLogLevelDefaultsToInfoAndRefusesAMisspelling` pins the default, validation and the env name. `make generate-config-reference` regenerated `config.example.yaml` and `docs/src/content/docs/operate/configuration-reference.md`; `make generate-config-reference-check` passes. The line also respects the global `log.level`.

# Implementation Plan

Log in the engine's terminal path (`fail`/`complete`) with slog fields, controlled by level.

# Notes

Related tickets: BUG-y57cz4

Related (from the audit): BUG-y57cz4 (done, observability)

# Related Files

`$SP/kf-server.log`.

# Attachments

## Progress (2026-10-16)

- Where it is emitted, and why that covers every trigger path: `Service.settled` in `internal/engine/terminal_log.go`, the only function that writes the line (`logTerminal`) and the only one that publishes a terminal execution event (`publishTerminal`). Manual, webhook, schedule, poll and retry runs differ only in how they are queued: each is a row in the same queue, claimed by `Service.runOnce`, which finishes it through `settled`. The other routes to a terminal state all call it too: an inline sub-workflow call (`persistChild`, with `parentExecution` set; `TestASubWorkflowChildWritesItsOwnLineNamingItsParent`), a trace that could not be persisted (`failPersist`), a wait the sweeper expired (`SweepWaits`), and a queued or waiting run cancelled before a worker saw it (`Service.Cancel`). The line is written from the record the store returned after the terminal write, so a cancellation that beat a finishing run is logged as cancelled. `TestOnlyTheSettleHelperPublishesTerminalEventsAndLogsTheLine` fails if any other engine file mentions a terminal event type or the log message, so a new terminal path cannot bypass it.
- Failing node: the last failed node run on the persisted record (a tolerated failure earlier in the run is not mistaken for the one that ended it); for an expired wait it is the node the wait held. Empty when the run failed outside any node (timeout between nodes, trace not persisted).
- Workflow name: taken from the document the worker already holds; a run cancelled while queued or waiting has no document in hand, so it falls back to the workflow's active revision and the name is omitted when there is none. The workflow id is always on the line.
- Repeated cancel requests for an already cancelled run keep re-announcing the live event as before but write no second line (the prior state is read first).
- The recovered-cancellation path in `runOnce` (a `cancelling` run whose worker lease expired) logs through `logTerminal` without publishing a live event, which it never did.
- Crashed executions (follow-up closed 2026-10-16): `settleCrashedExecution` fails an execution abandoned by its worker past the reclaim cap inside `ClaimNext`, so the engine never held it. The store now reports each one it ended, after the claim transaction commits, through `GORMExecutionStore.OnCrashSettled` (`internal/repository/executions.go`); `Service.watchCrashSettlements` registers `logTerminal` on it when the store offers the hook, so the run logs WARN, `status=failed`, `errorCode=execution.crashed`. Nothing new is published for it, as before. Tests: `TestExecutionStoreReportsACrashSettledExecutionToItsHook` (once, after commit, not before the cap) and `TestACrashSettledExecutionWritesAWarnLine`. The guard test now also forbids `publishTerminal(` outside `terminal_log.go` (the cancel path goes through `announce`). With several services sharing one store the last one registered receives the crash lines; production runs one per process.
- Verified: `go test ./internal/engine/ ./internal/config/ ./cmd/...`, `go test ./... -skip TestLuxonMatchesTheRecordedNodeGoldens`, `make generate-config-reference-check`.
