---
id: BUG-hfhzq6
title: Skip/fail/retry twice in one execution breaks trace persistence; execution re-runs forever
status: todo
priority: critical
labels:
    - engine
    - persistence
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T12:06:09Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:engine-runtime.

---
### Node skipped, failed or retried twice in one execution breaks trace persistence (duplicate key), so the execution stays running and is re-run from scratch every lease period, forever [find:engine-runtime] (critical/bug) · area: engine/service persistence + runner NodeRun rows · confidence: high

The runner never sets RunIndex on skipped rows (runner.go:445), failed/retry rows (:474, :511, :517, :528) or continueOnFail rows (:538), so they are all written with run_index 0. The unique index uidx_node_runs_attempt(execution_id,node_id,attempt,run_index) then rejects the second such row for a node inside a loop. runOnce returns the error, and the execution stays `running` with its lease. When the lease expires, ClaimNext reclaims it, deletes the trace and re-runs the whole graph. It hits the same collision on every re-run, so this repeats forever.

Evidence: Private instance :8101 (default_timeout 15s). Script: <W>/t_ifloop_noop.py, where <W> = /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/engine-runtime. It imports the n8n workflow Trigger -> SplitOut(arr) -> SplitInBatches v3 -> IF(arr != 2) -> A | B -> Loop, and runs it with {"arr":[1,3,4]}. B is skipped in iterations 1 and 2. server.log then shows `worker iteration failed ... persist node "b" run: create execution node run: duplicated key not allowed` every 15 s. The execution stays `running` with 9 partial node runs. Variant <W>/t_ifloop_side.py adds Done -> HTTP POST stub /side-effect. The stub is hit 3x at 15:34:46, 3x at 15:35:01, and so on (12 calls in 45 s) until the run is cancelled. Other triggers seen: (1) a loop that exceeds maxIterations (its failure row collides with loop run 0; see the 100-batch finding); (2) a de

n8n behavior: The IF-inside-Loop-Over-Items pattern runs once and finishes. Skipped branches are simply not executed, and nothing is re-run.

Impact: This hits every workflow with an IF/Switch inside a Loop Over Items with 3 or more iterations (13/100 top templates use splitInBatches v3), and any node inside a loop that fails, retries or uses continueOnFail from iteration 2 on. Side effects (emails, payments, API writes) repeat every 60 s (stock timeout) indefinitely, and workers are consumed. The run never reaches a terminal state.

Suggested fix: Stamp RunIndex on every NodeRun row: for skipped rows use the index after append; for failure, retry and tolerated rows use len(runs[nodeID]). Make trace-persist failure terminal: mark the execution failed with a persist error instead of leaving it running for reclaim. Add a reclaim/attempt counter with a poison-pill cap. Add a regression test for IF-in-loop and fail-in-iteration-3 (FEAT-sar60r acceptance claimed the latter).

Files: internal/engine/runner.go, internal/engine/service.go, internal/repository/executions.go, internal/repository/models.go

Existing tickets: FEAT-sar60r, FEAT-9knk67, FEAT-k3grr5

## Acceptance criteria

- [ ] Node skipped, failed or retried twice in one execution breaks trace persistence (duplicate key), so the execut
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)