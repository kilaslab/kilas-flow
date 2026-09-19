---
id: BUG-xf1wqm
title: Execution + history retention never runs (sweeper call sites deleted)
status: doing
priority: high
labels:
    - ops
    - retention
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T14:24:35Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:unfinished-work.

---
### Regression: execution retention and workflow-history retention never run (sweeper call sites deleted by merge 9132e09) [find:unfinished-work] (high/bug) · area: ops / retention · confidence: high

The retention settings execution.retention, history.retention and history.max_versions do nothing. A squash merge deleted the two lines that started the execution pruner and the history sweeper. Both functions are still defined but nothing calls them, so execution rows, node-run payloads, binary files and version history grow without limit even when an operator configures retention.

Evidence: `git show 9132e09 -- cmd/kilasflow/main.go` removes `startHistorySweeper(ctx, cfg.History, workflows, log)` and `startExecutionPruner(ctx, cfg.Execution, executions, runtime.DiscardBinaries, log)`. Today cmd/kilasflow/main.go:665 (startHistorySweeper) and :714 (startExecutionPruner) have zero callers; scanning every function in cmd/kilasflow shows these two as the only unused non-test functions besides workflowEnvironment. engine.Service.DiscardBinaries (internal/engine/service.go:193) therefore has no caller either. Meanwhile config.example.yaml:266-285 and docs/operate/configuration-reference.md:590-600 promise 'Retention deletes an execution ... along with its node runs and its stored binary payloads'. Other pages still say retention does not exist at all (docs/concepts/safety-boundaries.md:244, docs/concepts/execution-model.md:221), so the docs contradict each other.

n8n behavior: EXECUTIONS_DATA_PRUNE and EXECUTIONS_DATA_MAX_AGE prune executions and their binary data.

Impact: Every production install: the database and binary store grow without bound, and operators who turn retention on are misled. FEAT-5fv8gf's retention work is silently undone.

Suggested fix: Put both calls back in run(). Add a boot test that sets retention and checks the sweepers start. Add staticcheck U1000 (unused code) to CI; ci.yml only runs go vet and gofmt. Make the docs agree.

Files: cmd/kilasflow/main.go, internal/engine/service.go, internal/repository/execution_retention.go, config.example.yaml

Existing tickets: FEAT-5fv8gf, FEAT-ajw7wt

## Acceptance criteria

- [ ] Regression: execution retention and workflow-history retention never run (sweeper call sites deleted by merge 
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress 2026-09-19 (SecurityDx)
- Landed 2e43b78: restored startHistorySweeper + startExecutionPruner call-sites in run() (after scheduler, before API-only gate) + cmd/kilasflow/retention_test.go (TestRetentionSweepersPruneExpiredRows, TestRetentionSweepersAreWired). Scoped: go test ./cmd/kilasflow/ -run TestRetention PASS.
