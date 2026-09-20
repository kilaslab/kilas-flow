---
id: BUG-fng4m2
title: Login-spray test is wall-clock dependent and flakes under load
status: done
priority: high
labels:
    - ci
    - testing
created: "2026-09-20T17:05:00Z"
updated: "2026-09-20T10:51:55Z"
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

- [x] The test passes on a loaded machine (reproduce the load first: run it beside the rest of the module's suite) and still fails if the limiter stops refusing.
- [x] The limiter's behaviour is unchanged for production paths (`go test -race ./internal/api/... ./internal/api/middleware/...` green).
- [x] No test is skipped, deleted or given a longer attempt budget to get green.

## Implementation notes

The test asserted on wall-clock rate: it spent `3 * middleware.DefaultLoginAttemptsPerMinute` attempts and expected a refusal inside that count, so on a loaded box (one decoy hash per attempt, race detector, CPU contention) more than a minute elapsed and the per-minute bucket refilled before the budget ran out. The product was right; the test's premise was not.

Fix: the handler tests now drive a limiter whose clock is held still, so refill cannot intrude and the refusal is asserted on the attempt count — the limiter's actual contract — instead of on how long the attempts take.

- `internal/api/middleware/loginlimit.go`: added `(*LoginLimiter).WithClock(now func() time.Time) *LoginLimiter`, the smallest seam the handler package needs to stop the clock. It mirrors the existing `(*repository.GORMAuthStore).WithClock`; a nil clock is ignored and production never calls it (`NewLoginLimiter` still starts on the system clock), so no production knob or behaviour changed. `Allow`/`Fail`/`Succeed` are untouched.
- `internal/api/handlers/auth_test.go`: `loginTestHandler` injects `middleware.NewLoginLimiter(DefaultLoginAttemptsPerMinute).WithClock(...)` through the existing `WithLoginLimiter`. `waitForRefusal`'s bound dropped from `3 *` to one allowance `+ 1` (11 attempts) now that refill cannot happen; the comment was rewritten to say why the count is exact. No test was skipped, deleted, or given a longer budget; the bound got tighter.
- `internal/api/middleware/loginlimit_test.go`: `clockedLimiter` uses the new `WithClock` rather than assigning the unexported field directly.

This also removes the same latent wall-clock dependence from `TestLoginThrottlesByTheConnectedAddress`, which shares `loginTestHandler`.

Verification (all with the machine under four-way parallel landing load; `uptime` showed load averages 35-75 on a 10-core box):

- Reproduced first, unmodified: `go test -race -count=1 -v -run TestLoginRefusesASprayFromOneAddress ./internal/api/handlers/` → `auth_test.go:162: no refusal within 30 attempts`, `--- FAIL ... (134.93s)`.
- After the fix, same command under the same load → `--- PASS: TestLoginRefusesASprayFromOneAddress (63.54s)`; the log shows ten attempts admitted ~6s apart and the eleventh refused, i.e. the old 30-attempt budget was exactly the wall-clock race.
- Mutation proof: changed `Allow` to admit an empty bucket (`bucket.tokens >= 0`), reran → `auth_test.go:174: no refusal within 11 attempts`, `--- FAIL ... (93.65s)`; restored the original condition.
- `gofmt -l` on the three changed files printed nothing.
- `go build ./...` and `go vet ./...` → clean.
- `go test -race -count=1 ./internal/api/... ./internal/api/middleware/...` → `ok internal/api 222.6s`, `ok internal/api/handlers 256.1s`, `ok internal/api/middleware 1.7s`.
- `go test -count=1 ./internal/guardrails/...` → `ok github.com/kilaslab/kilas-flow/internal/guardrails 0.618s`.

### Review round 1 — the default limiter had no proof (this commit)

The targeted review found that after the fix every test in the package built its handler
through `loginTestHandler`, which injects its own limiter via `WithLoginLimiter`, so nothing
exercised the throttle `NewAuth` constructs by default. The reviewer verified on a copy that
`limiter: nil` kept the whole package green, while the pre-change suite caught exactly that
mutation.

Fix: added `TestLoginRefusesASprayWithTheDefaultLimiter` to `internal/api/handlers/auth_test.go`.
It builds `NewAuth(...)` with no `WithLoginLimiter`, asserts `handler.limiter` is non-nil, freezes
that limiter's own clock with `WithClock(func() time.Time { return stopped })`, then runs the
shared `waitForRefusal` spray from one address — no wall clock, count-based assertion.

Mutation proof on `internal/api/handlers/auth.go` (changed, ran, restored):

- `limiter: nil` → FAIL `auth_test.go:205: NewAuth() built no sign-in throttle; a nil one admits every attempt`.
- `limiter: middleware.NewLoginLimiter(middleware.DefaultLoginAttemptsPerMinute * 100)` → FAIL `auth_test.go:211: no refusal within 11 attempts`.
- restored `middleware.NewLoginLimiter(middleware.DefaultLoginAttemptsPerMinute)` → PASS (`ok github.com/kilaslab/kilas-flow/internal/api/handlers`).

Scoped gates for this commit:

- `gofmt -l internal/api/handlers/auth_test.go internal/api/handlers/auth.go` → nothing.
- `go vet ./...` → clean.
- `go build ./...` → clean.
- `go test -race -count=1 ./internal/api/... ./internal/api/middleware/... ./internal/guardrails/...` → all ok.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `67c6c900` (last commit at or before ticket created 2026-09-20)
- Commits (2):
  - `67c6c900` — BUG-fng4m2: hold the sign-in throttle's clock still so the login-spray test asserts on attempts
  - `1520a85a` — chore(pine): open BUG-fng4m2 — the login-spray test asserts on wall-clock rate
