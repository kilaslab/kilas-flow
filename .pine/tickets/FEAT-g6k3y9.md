---
id: FEAT-g6k3y9
title: Code-node JavaScript runs in worker processes, so a runaway built-in costs a worker, not the server
status: done
priority: high
parent: EPIC-tjnr1z
created: "2026-09-23T06:11:28Z"
updated: "2026-09-23T07:51:41Z"
---

# Description

goja cannot interrupt a single built-in call, and the heap watchdog can only
interrupt. The in-process guard against a built-in that allocates or loops as
far as a number tells it (`boundAllocations`) is therefore a denylist, and
every review of it found another way around it: a workflow author could hold
a core or take the whole server's memory in one call. The owner chose process
isolation, as n8n's task runners do, on 2026-09-23.

The server runs Code-node JavaScript in a pool of worker processes: the same
binary, re-executed. A runaway built-in now costs one worker, which the parent
kills and replaces. The in-process guards stay, as defence in depth that turns
the common cases into a readable RangeError instead of a killed worker.

# Acceptance Criteria
- [x] `jsrun.Runner.Run` is `Prepare` (in the parent: encode the input, check
      its cap, build the snapshot; compiles and runs nothing) plus `Execute`
      (on a fresh VM). A `Job`, a `Result` and every jsrun error cross a pipe
      and keep their meaning: `errors.Is` on each sentinel, and the
      ScriptError, SyntaxError and UnsupportedError fields.
- [x] `internal/jsworker`: a pool of at most `javascript_max_concurrent`
      workers, started on demand and reused; a two-way framed protocol so
      `$('Node')` and item pairing are answered by the parent while the code
      runs.
- [x] A worker that overruns a wall-clock backstop past the time limit is
      killed and the run fails with the time-limit sentence; one that dies
      mid-run fails the run with a named error (memory when the runtime said
      so), and the next run gets a fresh worker.
- [x] Cancelling the execution kills the worker running it.
- [x] Linux: each worker has an address-space rlimit, `oom_score_adj` 1000 and
      a parent-death signal. Everywhere: a minimal environment (none of the
      server's secrets), and it exits when its stdin closes.
- [x] `cmd/kilasflow` runs as a worker when started as one, before reading
      config, and the server uses the pool. Tests and the corpus keep the
      in-process runner.
- [x] Guardrail: `internal/jsworker` does not import goja; jsrun still
      imports no os, os/exec or syscall.
- [x] Docs: the execution model and configuration reference describe the
      workers.

# Implementation Plan

# Notes

- **7ea2814.** The pool, the protocol, the Linux limits and the server
  wiring. Smoke-tested on the built binary: one worker served both runs of a
  workflow, about 32 MB resident, three environment variables, gone after
  shutdown.
- **Security review (2026-09-23), fixed in 129e344.** The lineage and file
  references crossed to the worker uncounted by the input cap (an author could
  cost the server hundreds of MiB), and the server trusted a worker's result.
  Now only file IDs and the item count cross, the whole job header counts
  against the cap, results come back as the JSON the code returned and are
  decoded and checked in the server (output and console caps, files), every
  frame carries the job's nonce, and a dead worker is reported by what
  actually happened. The server keeps no compiled program, and a compiler
  panic fails the body. On Linux the server is undumpable and workers run
  with no_new_privs from /proc/self/exe, with a 3 GiB address-space
  baseline.
- **Not a privilege boundary.** Workers run as the server's user; the docs say
  so. FEAT-21h6xp would make them one (their own user, namespaces, seccomp,
  per-tenant workers).

# Related Files

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `b527da25` (last commit at or before ticket created 2026-09-23)
- Commits (3):
  - `e03cea3a` — EPIC-tjnr1z: notes: the worker review, and the binary size with every lane in
  - `129e3449` — FEAT-g6k3y9: review fixes: the server trusts a worker only as far as its code could go
  - `7ea2814c` — FEAT-g6k3y9: Code-node JavaScript runs in worker processes, so a runaway built-in costs a worker, not the server
- Files changed (base → working tree):

```
 .pine/tickets/BUG-14gp8r.md                        |   68 +
 .pine/tickets/BUG-548bk9.md                        |   46 +
 .pine/tickets/BUG-fthahg.md                        |   58 +
 .pine/tickets/FEAT-21h6xp.md                       |   45 +
 .pine/tickets/FEAT-9we7kw.md                       |   38 +
 .pine/tickets/FEAT-g6k3y9.md                       |   76 +
 .pine/tickets/FEAT-pxcbqj.md                       |   19 +
 .pine/tickets/FEAT-yxhgeh.md                       |  415 ++-
 .pine/tickets/FEAT-zjrw76.md                       |  421 ++-
 README.md                                          |    2 +-
 cmd/kilasflow/main.go                              |   46 +-
 config.example.yaml                                |   22 +-
 docs/src/content/docs/concepts/architecture.md     |    4 +-
 .../src/content/docs/concepts/safety-boundaries.md |   97 +-
 docs/src/content/docs/guides/community-nodes.md    |    4 +-
 docs/src/content/docs/guides/n8n-migration.md      |  212 +-
 .../docs/operate/configuration-reference.md        |   27 +-
 docs/src/content/docs/operate/deployment.md        |   11 +
 docs/src/content/docs/start/what-kilasflow-is.md   |    6 +-
 e2e/fixtures/n8n-live.ts                           |    1 +
 e2e/helpers/seed.ts                                |   12 +
 e2e/helpers/server.ts                              |    4 +
 e2e/tests/js-code.spec.ts                          |  695 +++++
 e2e/tests/n8n-compare.spec.ts                      |   22 +-
 e2e/tests/node-coverage.spec.ts                    |   25 +-
 go.mod                                             |    9 +-
 go.sum                                             |   12 +-
 internal/config/config.go                          |   29 +-
 internal/engine/item_outcomes.go                   |   99 +
 internal/engine/item_outcomes_test.go              |   98 +
 internal/engine/runindex_skip_test.go              |   46 +
 internal/engine/runner.go                          |   37 +-
 internal/guardrails/jsruntime_boundary_test.go     |   14 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |   23 +-
 internal/interop/n8n/parameters.go                 |   26 +-
 internal/jsrun/analyze.go                          |   58 +-
 internal/jsrun/analyze_test.go                     |    7 +-
 internal/jsrun/bounds_test.go                      |  281 ++
 internal/jsrun/buffer_test.go                      |   41 +
 internal/jsrun/console.go                          |    9 +-
 internal/jsrun/console_test.go                     |  103 +
 internal/jsrun/crypto.go                           |  859 ++++++
 internal/jsrun/crypto_test.go                      |  792 +++++
 internal/jsrun/doc.go                              |   21 +-
 internal/jsrun/engine.go                           |   16 +-
 internal/jsrun/errors.go                           |   20 +
 internal/jsrun/guards_test.go                      |   56 +-
 internal/jsrun/intl.go                             | 3109 ++++++++++++++++++++
 internal/jsrun/intl_test.go                        |  580 ++++
 internal/jsrun/items.go                            |  102 +-
 internal/jsrun/js/modules/buffer.js                |   59 +-
 internal/jsrun/js/modules/crypto.js                |  811 +++++
 internal/jsrun/js/modules/intl.js                  |  532 ++++
 internal/jsrun/js/modules/luxon.js                 |  110 +
 internal/jsrun/js/runtime.js                       | 1153 +++++++-
 internal/jsrun/jsrun.go                            |   25 +-
 internal/jsrun/jsrun_test.go                       |    6 +-
 internal/jsrun/libraries.go                        |    2 +
 internal/jsrun/luxon_test.go                       |  163 +
 internal/jsrun/modules.go                          |    8 +-
 internal/jsrun/programs.go                         |   43 +-
 internal/jsrun/roots.go                            |   28 +-
 internal/jsrun/roots_test.go                       |   61 +-
 internal/jsrun/run.go                              |  287 +-
 internal/jsrun/testdata/parity/collation.json      |   10 +
 internal/jsrun/testdata/parity/currencies.json     |   59 +
 internal/jsrun/testdata/parity/date-options.json   | 1804 ++++++++++++
 internal/jsrun/testdata/parity/dates.json          |   22 +
 internal/jsrun/testdata/parity/luxon.json          |   61 +
 internal/jsrun/testdata/parity/numbers.json        |  317 ++
 internal/jsrun/testdata/parity/weeks.json          |   28 +
 internal/jsrun/testdata/parity/zones.json          |  604 ++++
 internal/jsrun/watchdog.go                         |    4 +-
 internal/jsrun/wire.go                             |  200 ++
 internal/jsrun/wire_test.go                        |   57 +
 internal/jsworker/doc.go                           |   53 +
 internal/jsworker/jsworker_test.go                 |  448 +++
 internal/jsworker/limits_linux.go                  |   87 +
 internal/jsworker/limits_other.go                  |   29 +
 internal/jsworker/norace.go                        |    6 +
 internal/jsworker/pool.go                          |  577 ++++
 internal/jsworker/protocol.go                      |  132 +
 internal/jsworker/race.go                          |    6 +
 internal/jsworker/worker.go                        |  133 +
 nodes/executors.go                                 |   12 +-
 nodes/jscode.go                                    |   40 +-
 nodes/jscode_roots.go                              |    3 +
 nodes/jscode_roots_test.go                         |   24 +
 nodes/jscode_run_test.go                           |   41 +
 nodes/transform.go                                 |    2 +-
 scripts/js-parity/record.mjs                       |  482 +++
 skills/index.json                                  |    2 +
 skills/kilasflow-expressions/SKILL.md              |    5 +-
 skills/kilasflow-import-export/SKILL.md            |   15 +-
 .../references/MAPPING_LIMITS.md                   |   57 +-
 web/src/lib/workflow-editor/catalog.test.ts        |   12 +
 web/src/lib/workflow-editor/catalog.ts             |   10 +-
 97 files changed, 17030 insertions(+), 423 deletions(-)
```
