---
id: BUG-9s3htg
title: The Go Code node test exceeds its own time limit under the race detector
status: done
priority: high
created: "2026-09-05T15:41:56Z"
updated: "2026-09-05T16:59:27Z"
---

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

- [x] `make test` passes on a clean checkout, including under `-race`.
- [x] Whatever ceiling the test runs under is derived rather than hard-coded to
      a wall-clock number that is only right on one machine — a limit that is
      correct on a laptop and wrong on a runner will fail again the next time
      the ratio changes.
- [x] The fix does not weaken what the test proves: it still has to run real
      compiled Go once per item and once for all items, and still has to prove
      the node enforces *a* limit.

## References

- `nodes/code_test.go:288` — the assertion that reports the node's own error.
- `nodes/jscode.go` — a wrong guess when this was filed: that file is the
  placeholder for imported JavaScript and Python Code nodes and holds no limit.
  The 10s is `runcode.DefaultLimits().Timeout` in `internal/runcode/runcode.go`,
  applied by `Runner.Execute` and tightened per node by `scriptTimeoutSeconds`
  in `nodes/code.go`.
- FEAT-7tgasa — the CI pipeline, which is where this becomes everyone's problem.

## Work evidence

### Root cause

The 10s is `runcode.DefaultLimits().Timeout` in `internal/runcode/runcode.go`.
It is a deployment ceiling that a node may tighten and never raise
(`nodes/code.go`, `scriptTimeoutSeconds`, default 10). Nothing about it is wrong
as a number. What was wrong is what it was being spent on.

`Runner.Execute` opened `context.WithTimeout(ctx, limits.Timeout)` *before*
building the wazero runtime, and then called `InstantiateWithConfig`, which
compiles the module and runs it under that one deadline. Compiling is the
expensive half: the wasip1 artifact carries the Go runtime and is 4.8 MB, and
wazero translates the whole of it to machine code on every single call. Measured
per phase on this machine, `return items, nil`:

| phase | no `-race` | `-race` |
| --- | --- | --- |
| Go toolchain build (cached by source hash, 90s limit of its own) | 97ms | 94ms |
| wazero translation + instantiate + run, per `Execute` | 810ms | 11.5–12.5s |

So under `-race` the node spent 12s of a 10s budget before user code reached its
first instruction, and reported the user's program as the thing that overran.
The same arithmetic makes the bug reachable without `-race` on a slow enough
host, and it made the effective budget depend on module size and host load.

The second defect the first one hid: nothing reused the translation. The Code
node's own editor text promises that per-item mode "costs one build either way",
which was true of the Go build and false of the wazero one — a node over 100
items paid 100 translations. That is why the per-item subtest was the slower of
the two.

### Fix

- `Runner.Execute` builds the runtime, instantiates WASI and translates the
  module under the caller's context, and only then opens the deadline, around
  `InstantiateModule` alone. The limit now bounds the user's program and nothing
  else. The caller's context still bounds the translation, so it cannot hang.
- New `runcode.ModuleCache` wraps `wazero.CompilationCache`. `CodeExecutor`
  owns one for the life of the process and hands it to every per-call runner, so
  an artifact is translated once per process. Each execution still gets its own
  runtime, instantiated and closed on its own; only immutable machine code is
  shared. `NewRunner` takes it as a parameter and accepts nil.
- The compiled module is deliberately never closed — `CompiledModule.Close`
  deletes the translation from the shared engine, which is the thing being
  cached.
- Tests dropped the `generousLimits()` helper (120s) in both packages and run
  under `runcode.DefaultLimits()`. The suite now proves the shipped default is
  workable rather than working around it.

### What was rejected

- **Raising the limit, or deriving it from a benchmark of the host.** Both
  encode the assumption that translation belongs in the user's budget. A
  self-calibrating limit would also have had to be recalibrated the next time
  the ratio moved, which is the failure mode the acceptance criteria name.
- **Making the limit configurable by the test.** That is what the 120s
  `generousLimits()` already was, and it is why this went unnoticed until CI:
  the tests were passing because they were not running the shipped
  configuration.
- **Skipping under `-race`.** A skip reads as a pass, and the failure was a real
  product defect, not a test artefact.
- **A persistent on-disk `NewCompilationCacheWithDir`.** It would speed up a
  cold CI run further, but it needs a cache directory, an eviction story and a
  config surface, and it does not fix the deadline, which is the actual bug.
- **Running the wasm through the interpreter instead** (no translation at all):
  guest execution becomes orders of magnitude slower, which would trade this
  failure for the same failure in the tests that assert a limit fires.
- **Parallelising the `runcode` suite** to claw back CI time: two tests here
  assert on wall clock, and contention on a 2-vCPU runner would make them noisy.

### Timings measured (Apple M-series, 10 cores, go1.27.1)

`TestTheGoCodeNodeRunsOncePerItemWhenAsked`, the reported failure:

| | before | after |
| --- | --- | --- |
| no `-race` | 3.64s pass | 1.05s pass |
| `-race` | 25.49s **fail** (both subtests) | 12.50s pass |

Whole packages, `-count=1`:

| | before | after |
| --- | --- | --- |
| `go test ./nodes/` | 9.16s | 7.15s |
| `go test ./nodes/ -race` | 100.22s **fail** | 76.8–89.4s pass |
| `go test ./internal/runcode/` | 11.15s | 13.78s (three tests added) |
| `go test ./internal/runcode/ -race` | 147.23s | 158.65s (three tests added) |

Translation reuse, from `TestTheSameModuleIsTranslatedOncePerProcess`:
first run 944ms → second 24ms without `-race`; 11.37s → 277ms with it. Roughly
40x either way, which is why the test's threshold is a ratio against its own
first run rather than a wall-clock number.

Full `go test ./... -race -count=1` passes (2m48s). `go vet ./...` clean;
`gofmt -l .` empty outside `web/`.

### Tests

- `TestTheGoCodeNodeRunsOncePerItemWhenAsked` is unchanged in what it proves and
  still runs real compiled Go once per item and once for all items, now under
  the product's own `DefaultLimits()` — it is the regression test for this bug.
- `TestCodeNodeCannotRaiseTheDeploymentsLimits` and
  `TestExecutionStopsAtItsTimeLimit` still prove a genuinely over-running
  program is refused. The latter is strengthened: with translation out of the
  measurement its wall-clock bound went from 60s to 5s against a 500ms limit,
  so a limit that fires late now fails instead of passing.
- `TestTheTimeLimitIsNotSpentTranslatingTheModule` — three runs under a 1s
  limit, which only pass if translation is outside it.
- `TestTheSameModuleIsTranslatedOncePerProcess` — the reuse, as a ratio.
- `TestASharedTranslationDoesNotCarryAMemoryLimitWithIt` — sharing machine code
  between executions must not share the memory ceiling with it. wazero keys the
  engine cache on the module binary and decodes limits per runtime, so it does
  not; this pins that, because getting it wrong would let one node inherit
  another's roomier sandbox. It costs one translation (~11.5s under `-race`) and
  is worth it.
