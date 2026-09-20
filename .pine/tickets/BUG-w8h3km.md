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

- [x] Both tests pass repeatedly (at least 10 consecutive runs of the package) on a loaded machine, and still fail if the wait service stops resolving expired waits.
- [x] Production behaviour of the wait service is unchanged (`go test -race ./internal/engine/...` green).
- [x] No test weakened, skipped or given a longer wall-clock margin to get green.

## Implementation notes

Root cause: both tests minted their deadline with `time.Now()` and the durable wait
service validated/swept it against a *separate* `time.Now()` read. Under CPU
contention the gap between the two reads exceeded the 50 ms (and 250 ms) margin, so
`suspend` saw "the deadline is in the past" and failed the execution instead of
parking it. Reproduced on the loaded machine: 8 failures in 10 `-race` package runs
(same `0b8cadd`-era defect the ticket cites).

Fix — a clock/timer seam, not a bigger margin:

- `Service.now func() time.Time` (nil => wall clock), read through `Service.clock()`;
  used by `suspend` (validation), `SweepWaits`, `armWaitTimer` delay, and
  `ResumeWait`'s liveness check. One call site drives the whole wait lifetime.
- `waitTimers.afterFunc func(time.Duration, func()) *time.Timer` (nil =>
  `time.AfterFunc`), so a test owns when a suspension's exact wake-up fires.
- `internal/engine/export_test.go` exposes `SetClockForTest` and `SetTimerForTest`
  (unexported fields, test-only file, no production knob).
- Rewrote the two tests to own the clock and the timer: the deadline is minted and
  validated against the same clock, and the test advances the clock and fires the
  armed wake-up itself. The `elapsed > time.Second` latency assertion in
  `TestExpiredWaitsResolveOnTheirOwnDeadline` is gone; it is replaced by an exact
  assertion that the suspension armed its remaining deadline (250 ms / 50 ms) and
  that firing that wake-up alone requeues or fails the wait. No skip, no sleep, no
  widened margin was added.

Verification:

- Reproduction (pre-fix, load avg ~79): 8/10 `-race` package runs failed
  (e.g. `status = "failed", want waiting ... "the deadline is in the past"`).
- `for i in $(seq 1 12); do go test -race -count=1 ./internal/engine/; done`
  under 20 busy-loop load generators: 12/12 `ok`.
- Mutation: `SweepWaits` short-circuited to never settle an expired wait —
  `TestResumeOfAPerItemSuspendProcessesEveryItem` and
  `TestExpiredWaitsResolveOnTheirOwnDeadline` both FAIL (execution stuck
  `waiting`), then restored and green.
- `gofmt -l` clean; `go vet ./...` clean; `go build ./...` clean;
  `go test -count=1 ./internal/guardrails/...` ok.

## Review round 1 — production default was left untested

Finding (medium): the rewrite installed the clock and timer stubs in *both* remaining
deadline tests, so nothing exercised the production nil defaults —
`Service.clock()` falling back to `time.Now()` and `waitTimers.arm` falling back to
`time.AfterFunc`. A no-op scheduler in that fallback kept the whole package green;
production would then arm no wake-up at all and only the one-minute sweep would
settle a wait (a 2 s wait costing up to a minute, a webhook whose workflow waits
answering 504 first).

Fix — one test that leaves both seams nil and lets the real timer do the work:

- `TestDefaultSchedulerSettlesAWaitAtItsDeadline` builds the service with
  `waitTestService` (no `SetClockForTest`, no `SetTimerForTest`), suspends an
  interval wait whose node mints a 20 ms deadline at execution time
  (`waitSuspender.expiresAfter`, the shape a real wait node has when it turns a
  duration into a deadline), and asserts the exact transition: parked `waiting`,
  then `queued` via `awaitExecutionStatus` (500 ms window) with no `SweepWaits` call
  and no hand-fired wake-up, then a second worker resumes it to `succeeded`.
  No sleep, no latency threshold, no widened margin.
- `waitSuspender` gained the optional `expiresAfter` field; every existing use sets
  `expiresAt` and is unchanged.

Verification (this round):

- Mutation proving the test bites: nil fallback replaced with
  `func(time.Duration, func()) *time.Timer { return &time.Timer{} }` —
  `TestDefaultSchedulerSettlesAWaitAtItsDeadline` FAILS with
  `execution status = ("waiting", <nil>), want "queued" before timeout`, while the
  two seam-driven tests stay `ok` against the same mutation (exactly the gap the
  review found). Restored, production file byte-identical to `c0cca80`
  (`cmp` against a pre-mutation copy).
- Flake check: 10 × `go test -race -count=1 ./internal/engine/...` — 10/10 `ok`
  (31–80 s each) on a box whose load average was 48–62 from ~20 sibling worktrees
  running their own test binaries; then, with 8 owned busy-loop load generators
  running as well, 20 × `-run TestDefaultSchedulerSettlesAWaitAtItsDeadline`
  (20/20 `ok`) and 3 × the whole package (3/3 `ok`).
- `gofmt -l` on the changed file prints nothing; `go vet ./...` clean;
  `go build ./...` clean; `go test -race -count=1 ./internal/guardrails/...` ok.
- Never weakened, skipped or deleted an existing test: the only production file
  touched this round was the mutation, which was reverted.

