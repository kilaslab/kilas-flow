---
id: BUG-w8h3km
title: Wait-service tests flake under load with "the deadline is in the past"
status: doing
priority: high
labels:
    - ci
    - testing
created: "2026-09-20T17:25:00Z"
updated: "2026-09-20T17:25:00Z"
---

## Problem

Two tests in `internal/engine` fail intermittently on a loaded machine, which makes the whole-module gate unreliable during parallel landing runs:

- `TestResumeOfAPerItemSuspendProcessesEveryItem`
- `TestExpiredWaitsResolveOnTheirOwnDeadline` — fails with "the deadline is in the past"

## Evidence

- Hit while a dozen parallel worktree builds were running: the same two tests failed in a `-race` package run and were reported as 5 failures in 8 runs of the package. They pass in isolation on the same tree, and they fail identically on an older commit (`0b8cadd`), so the flake predates the change that observed it.
- The failure mode ("the deadline is in the past") suggests the test computes a deadline relative to wall-clock `time.Now()` and then asserts on it after work that can take longer than the margin under CPU contention — the same class of defect as BUG-fng4m2.

## Fix direction (verify before implementing)

Reproduce it on a loaded machine first and record the real output. Then remove the test's dependence on wall-clock margins — drive the wait service's clock or its timer directly (the service should already have a seam; if it does not, add the smallest honest one) rather than enlarging the margin. Do NOT add sleeps, do NOT skip under load, do NOT widen a timeout to hide the race: the properties "an expired wait resolves" and "a per-item suspend resumes every item" must stay proven.

## Acceptance criteria

- [ ] Both tests pass repeatedly (at least 10 consecutive runs of the package) on a loaded machine, and still fail if the wait service stops resolving expired waits.
- [ ] Production behaviour of the wait service is unchanged (`go test -race ./internal/engine/...` green).
- [ ] No test weakened, skipped or given a longer wall-clock margin to get green.
