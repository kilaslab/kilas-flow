---
id: BUG-p3t7yq
title: The concurrent-migration-start test flakes under load with "duplicated key not allowed"
status: doing
priority: high
labels:
    - ci
    - testing
created: "2026-09-20T21:40:00Z"
updated: "2026-09-20T21:40:00Z"
---

## Problem

`internal/database`'s `TestConcurrentStartsApplyTheBaselineExactlyOnce` fails intermittently when the machine is loaded (a dozen parallel worktree builds), with a `duplicated key not allowed` error. It is the third test of this class found while landing tickets on 2026-09-20, after BUG-fng4m2 (login spray) and BUG-w8h3km (wait deadlines).

## Evidence

- Hit during the landing gate of BUG-fng4m2 (landing commit 67c6c90): `go test ./...` run 1 failed in `internal/database` with that error; a second full run was green, and the test passes in isolation (`-count=1` ok in 1.039s).
- Proven pre-existing, not caused by that landing: `go test -count=30 -run TestConcurrentStartsApplyTheBaselineExactlyOnce ./internal/database/` was run in a clean checkout of the parent commit `72d0200` and failed with the same `duplicated key not allowed` error.

## Fix direction (verify before implementing)

Reproduce it on a loaded machine, then find the true cause. The name says "exactly once": if the test's premise is that N concurrent starts race to apply a baseline migration, it must tolerate the loser's failure mode rather than depending on a scheduling outcome. Either the production code must make the race genuinely impossible (a unique violation being retried/absorbed is the normal shape) or the test must assert the invariant that actually matters (the migration applied exactly once and the loser's error is the documented one) instead of asserting on an unordered race outcome. Decide with evidence, state which, and do NOT add sleeps, do NOT skip under load, do NOT widen a timeout.

## Acceptance criteria

- [ ] The package passes at least 30 consecutive `-count=30` runs (and 10 consecutive `-race` runs) on a loaded machine.
- [ ] The invariant still holds: the baseline migration is applied exactly once, and a concurrent starter either succeeds or fails with the documented error.
- [ ] No test weakened, skipped, or given a longer timeout to get green.
