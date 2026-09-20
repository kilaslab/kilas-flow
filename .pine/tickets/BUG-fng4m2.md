---
id: BUG-fng4m2
title: Login-spray test is wall-clock dependent and flakes under load
status: doing
priority: high
labels:
    - ci
    - testing
created: "2026-09-20T17:05:00Z"
updated: "2026-09-20T17:05:00Z"
---

## Problem

`internal/api/handlers/auth_test.go:151` (`TestLoginRefusesASprayFromOneAddress`) fails whenever the machine is busy, which means it fails during parallel landing gates and in CI under load. It was hit twice while landing unrelated tickets on 2026-09-20.

## Evidence

- Reproduced alone on a loaded machine (a dozen parallel worktree builds): `go test -race -count=1 -run TestLoginRefusesASprayFromOneAddress ./internal/api/handlers/` → FAIL after 179.61s, "no refusal within 30 attempts".
- The test spends its whole budget of `3 * middleware.DefaultLoginAttemptsPerMinute` (= 30) attempts and expects a refusal inside that count. Each attempt costs ~5s under load (bcrypt + race detector + CPU contention), so more than 60 seconds elapse and the per-minute token bucket refills before the budget is exhausted.
- Nothing about the product is wrong: the limiter is doing exactly what a per-minute bucket does. The test asserts on wall-clock rate, not on the limiter's contract.

## Fix direction (verify before implementing)

Make the assertion time-independent, e.g. by driving the limiter with an injected clock (the middleware already needs a seam for this) or by asserting on the refusal decision for a fixed sequence of attempts inside a single window rather than on how long 30 attempts take on a loaded box. Do NOT raise the budget, do NOT add a sleep, do NOT skip the test under load: the property "a spray from one address is refused" must stay proven.

## Acceptance criteria

- [ ] The test passes on a loaded machine (reproduce the load first: run it beside the rest of the module's suite) and still fails if the limiter stops refusing.
- [ ] The limiter's behaviour is unchanged for production paths (`go test -race ./internal/api/... ./internal/api/middleware/...` green).
- [ ] No test is skipped, deleted or given a longer attempt budget to get green.
