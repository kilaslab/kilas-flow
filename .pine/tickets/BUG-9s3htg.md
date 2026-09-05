---
id: BUG-9s3htg
title: The Go Code node test exceeds its own time limit under the race detector
status: todo
priority: high
created: "2026-09-05T15:41:56Z"
updated: "2026-09-05T15:41:56Z"
---

# Description

# Steps to Reproduce

# Expected

# Actual

# Acceptance Criteria
- [ ] Define acceptance criteria

# Related Files

# Attachments

## Scope

`make test` runs `go test ./... -race`, and under the race detector
`TestTheGoCodeNodeRunsOncePerItemWhenAsked` fails both its subtests:

```
--- FAIL: TestTheGoCodeNodeRunsOncePerItemWhenAsked (23.14s)
    --- FAIL: .../runOnceForAllItems  (11.55s) code_test.go:288: code exceeded its 10s time limit
    --- FAIL: .../runOnceForEachItem  (11.49s) code_test.go:288: code exceeded its 10s time limit
```

Without `-race` the same test takes about 4 seconds and passes. The limit is
the Code node's own 10-second ceiling, not the test framework's, so the failure
is the node refusing its own work rather than the harness giving up: compiling
and running Go to wasm through wazero is several times slower with the race
detector instrumenting the host side.

It is pre-existing and independent of any current work — it reproduces on a
clean checkout of `main` — and it was found while FEAT-7tgasa was making the
repository run its checks in CI. That matters because a 2-vCPU GitHub runner is
slower than this machine, so the pipeline this repository is about to gain
would be red on its first run for a reason that has nothing to do with the
change under test.

## Acceptance criteria

- [ ] `make test` passes on a clean checkout, including under `-race`.
- [ ] Whatever ceiling the test runs under is derived rather than hard-coded to
      a wall-clock number that is only right on one machine — a limit that is
      correct on a laptop and wrong on a runner will fail again the next time
      the ratio changes.
- [ ] The fix does not weaken what the test proves: it still has to run real
      compiled Go once per item and once for all items, and still has to prove
      the node enforces *a* limit.

## References

- `nodes/code_test.go:288` — the assertion that reports the node's own error.
- `nodes/jscode.go` / the Code node's time limit, which is what the 10s belongs to.
- FEAT-7tgasa — the CI pipeline, which is where this becomes everyone's problem.
